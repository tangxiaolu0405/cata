package ui

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cata/internal/cata/client"
	"cata/internal/gateway"
	"cata/internal/gateway/tunnel"
)

// WebChat 管理 web:<project_id> 会话。
type WebChat struct {
	cfg      gateway.Config
	sessions *gateway.SessionManager
	locks    *gateway.ProcessLock
	remote   *tunnel.Registry // 非 nil = remote 模式：经隧道拨远端 agent

	mu       sync.Mutex
	pendingE map[string]chan bool
	pendingC map[string]chan []string
	pathLock map[string]func() // project path unlockers while session open
}

// NewWebChat 创建 web 聊天管理器（本地模式：拨本机 per-ws / legacy socket）。
func NewWebChat(cfg gateway.Config) *WebChat {
	return &WebChat{
		cfg:      cfg,
		sessions: gateway.NewSessionManager(cfg.SocketPath, cfg.WorkerRoot),
		locks:    gateway.NewProcessLock(),
		pendingE: make(map[string]chan bool),
		pendingC: make(map[string]chan []string),
		pathLock: make(map[string]func()),
	}
}

// NewWebChatWithRegistry 创建 web 聊天管理器（remote 模式：项目 = 在线 agent，
// 会话经隧道拨到对应 agent 的 per-ws socket）。
func NewWebChatWithRegistry(cfg gateway.Config, reg *tunnel.Registry) *WebChat {
	return &WebChat{
		cfg:      cfg,
		sessions: gateway.NewRemoteSessionManager(cfg.WorkerRoot, gateway.NewCataConn),
		locks:    gateway.NewProcessLock(),
		remote:   reg,
		pendingE: make(map[string]chan bool),
		pendingC: make(map[string]chan []string),
		pathLock: make(map[string]func()),
	}
}

// Remote 是否运行在 remote 模式。
func (w *WebChat) Remote() bool { return w.remote != nil }

func webSessionKey(projectID string) gateway.SessionKey {
	return gateway.SessionKeyFor("web", projectID)
}

// EnsurePathLock 占用项目产出区锁（与 TUI 互斥）。
func (w *WebChat) EnsurePathLock(projectID, absPath string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.pathLock[projectID]; ok {
		return nil
	}
	unlock, err := client.AcquireOutputLock(absPath)
	if err != nil {
		return err
	}
	w.pathLock[projectID] = unlock
	return nil
}

// ReleasePathLock 释放项目锁。
func (w *WebChat) ReleasePathLock(projectID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if u, ok := w.pathLock[projectID]; ok {
		u()
		delete(w.pathLock, projectID)
	}
}

// Close 关闭全部连接与锁。
func (w *WebChat) Close() {
	w.sessions.CloseAll()
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, u := range w.pathLock {
		u()
		delete(w.pathLock, id)
	}
}

// StreamEvent 推给浏览器的 NDJSON 事件。
type StreamEvent map[string]any

// Chat 对某项目发一轮对话；emit 写流式事件。
func (w *WebChat) Chat(ctx context.Context, project gateway.Project, text string, emit func(StreamEvent) error) (gateway.ChatResult, error) {
	abs, err := filepath.Abs(strings.TrimSpace(project.Path))
	if err != nil {
		return gateway.ChatResult{}, err
	}
	if err := w.EnsurePathLock(project.ID, abs); err != nil {
		return gateway.ChatResult{}, err
	}

	key := webSessionKey(project.ID)
	unlock := w.locks.Lock(key)
	defer unlock()

	conn, err := w.sessionConn(key, abs, project)
	if err != nil {
		return gateway.ChatResult{}, err
	}
	h := &webStreamHandler{w: w, ctx: ctx, emit: emit, projectID: project.ID}
	return conn.Chat(ctx, text, h)
}

// sessionConn 获取会话连接：remote 模式经隧道拨对应在线 agent；
// 本地模式按 project.ID(=ws_id) 拨本机对应 agent 的 per-ws socket（EnsureAgent 按需拉起）。
// 两者对称——都是「按项目路由到对应 agent」，只差 dialFunc 实现。
func (w *WebChat) sessionConn(key gateway.SessionKey, abs string, project gateway.Project) (*gateway.CataConn, error) {
	if w.remote != nil {
		return w.sessions.GetWithCwdDialer(key, abs, func() (net.Conn, error) {
			return w.remote.DialAgent(project.ID)
		})
	}
	return w.sessions.GetWithCwdDialer(key, abs, gateway.DialLocalAgent(project.ID))
}

// Reset 清空项目会话。
func (w *WebChat) Reset(project gateway.Project) error {
	abs, err := filepath.Abs(strings.TrimSpace(project.Path))
	if err != nil {
		return err
	}
	key := webSessionKey(project.ID)
	unlock := w.locks.Lock(key)
	defer unlock()
	conn, err := w.sessionConn(key, abs, project)
	if err != nil {
		return err
	}
	return conn.Reset()
}

// ResolveConfirm 供 HTTP 回写 exec_confirm。
func (w *WebChat) ResolveConfirm(confirmID string, approved bool) error {
	w.mu.Lock()
	ch := w.pendingE[confirmID]
	delete(w.pendingE, confirmID)
	w.mu.Unlock()
	if ch == nil {
		return fmt.Errorf("unknown confirm_id")
	}
	select {
	case ch <- approved:
		return nil
	default:
		return fmt.Errorf("confirm already resolved")
	}
}

// ResolveChoice 供 HTTP 回写 user_choice。
func (w *WebChat) ResolveChoice(choiceID string, selected []string) error {
	w.mu.Lock()
	ch := w.pendingC[choiceID]
	delete(w.pendingC, choiceID)
	w.mu.Unlock()
	if ch == nil {
		return fmt.Errorf("unknown choice_id")
	}
	select {
	case ch <- selected:
		return nil
	default:
		return fmt.Errorf("choice already resolved")
	}
}

type webStreamHandler struct {
	w         *WebChat
	ctx       context.Context
	emit      func(StreamEvent) error
	projectID string
}

func (h *webStreamHandler) OnToken(s string) {
	_ = h.emit(StreamEvent{"type": "token", "content": s})
}

func (h *webStreamHandler) OnProgress(message string) {
	_ = h.emit(StreamEvent{"type": "progress", "message": message})
}

func (h *webStreamHandler) OnToolStart(name string) {
	_ = h.emit(StreamEvent{"type": "tool_start", "name": name})
}

func (h *webStreamHandler) ConfirmExec(ctx context.Context, p gateway.ExecConfirmPrompt) (bool, error) {
	ch := make(chan bool, 1)
	h.w.mu.Lock()
	h.w.pendingE[p.ConfirmID] = ch
	h.w.mu.Unlock()
	_ = h.emit(StreamEvent{
		"type":         "exec_confirm_required",
		"confirm_id":   p.ConfirmID,
		"command_line": p.CommandLine,
		"cwd":          p.Cwd,
		"project_id":   h.projectID,
	})
	select {
	case <-ctx.Done():
		h.w.mu.Lock()
		delete(h.w.pendingE, p.ConfirmID)
		h.w.mu.Unlock()
		return false, ctx.Err()
	case ok := <-ch:
		return ok, nil
	case <-time.After(10 * time.Minute):
		h.w.mu.Lock()
		delete(h.w.pendingE, p.ConfirmID)
		h.w.mu.Unlock()
		return false, fmt.Errorf("exec confirm timeout")
	}
}

func (h *webStreamHandler) Choose(ctx context.Context, p gateway.UserChoicePrompt) ([]string, error) {
	ch := make(chan []string, 1)
	h.w.mu.Lock()
	h.w.pendingC[p.ChoiceID] = ch
	h.w.mu.Unlock()
	opts := make([]map[string]string, 0, len(p.Options))
	for _, o := range p.Options {
		opts = append(opts, map[string]string{"id": o.ID, "label": o.Label, "desc": o.Desc})
	}
	_ = h.emit(StreamEvent{
		"type":       "user_choice",
		"id":         p.ChoiceID,
		"prompt":     p.Prompt,
		"multi":      p.Multi,
		"options":    opts,
		"project_id": h.projectID,
	})
	select {
	case <-ctx.Done():
		h.w.mu.Lock()
		delete(h.w.pendingC, p.ChoiceID)
		h.w.mu.Unlock()
		return nil, ctx.Err()
	case sel := <-ch:
		return sel, nil
	case <-time.After(10 * time.Minute):
		h.w.mu.Lock()
		delete(h.w.pendingC, p.ChoiceID)
		h.w.mu.Unlock()
		return nil, fmt.Errorf("user choice timeout")
	}
}
