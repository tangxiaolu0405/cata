package api

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"cata/internal/cata/config"
	"cata/internal/cata/socketclient"
)

// Worker 桥接 HTTP 与 socket server：按会话维护 socketclient.Conn（保留 history），
// 并管理交互回写（exec_confirm / user_choice）的挂起 handler。
//
// 与 gateway.SessionManager 语义对齐：会话键 = session_id；连接复用；断线自动重拨。
type Worker struct {
	// AgentID 目标工作空间 id（= ws_id，拨其 per-ws socket）。
	AgentID string
	// Cwd 产出区（工作空间根路径）；空 = 默认 worker 目录。
	Cwd string
	// Dial 自定义拨号器（测试用）；nil = 拨本机 per-ws socket。
	Dial func() (net.Conn, error)

	mu       sync.Mutex
	conns    map[string]*socketclient.Conn // session_id → 连接
	handlers map[string]*httpStreamHandler // session_id → 当前挂起的交互 handler
}

// NewWorker 创建 worker。agentID 为目标工作空间 id。
func NewWorker(agentID, cwd string) *Worker {
	return &Worker{
		AgentID:  agentID,
		Cwd:      cwd,
		conns:    make(map[string]*socketclient.Conn),
		handlers: make(map[string]*httpStreamHandler),
	}
}

// dialFunc 返回拨号器：自定义优先，否则拨本机 per-ws socket。
func (w *Worker) dialFunc() func() (net.Conn, error) {
	if w.Dial != nil {
		return w.Dial
	}
	agentID := strings.TrimSpace(w.AgentID)
	if agentID == "" {
		return nil // 默认 socket
	}
	return func() (net.Conn, error) {
		return net.DialTimeout("unix", config.ResolvedAgentSocketPath(agentID), 5*time.Second)
	}
}

// Conn 获取（或创建）指定会话的连接。cwd 非空时覆盖默认产出区。
func (w *Worker) Conn(ctx context.Context, sessionID, cwd string) (*socketclient.Conn, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if c, ok := w.conns[sessionID]; ok {
		if c.Healthy() {
			return c, nil
		}
		log.Printf("api worker: session %s conn unhealthy, rebuilding", sessionID)
		_ = c.Close()
		delete(w.conns, sessionID)
	}
	useCwd := cwd
	if strings.TrimSpace(useCwd) == "" {
		useCwd = w.Cwd
	}
	var c *socketclient.Conn
	if d := w.dialFunc(); d != nil {
		c = socketclient.NewConnWithDialer("", useCwd, d)
	} else {
		c = socketclient.NewConn("", useCwd)
	}
	w.conns[sessionID] = c
	return c, nil
}

// attachHandler 挂起会话的交互 handler（/v1/chat 开始时）。
func (w *Worker) attachHandler(sessionID string, h *httpStreamHandler) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.handlers[sessionID] = h
}

// detachHandler 移除会话的交互 handler（/v1/chat 结束时）。
func (w *Worker) detachHandler(sessionID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.handlers, sessionID)
}

// Resolve 回写交互（/v1/resolve）：exec 确认或 choice 选择。
func (w *Worker) Resolve(sessionID string, req resolveRequest) error {
	w.mu.Lock()
	h := w.handlers[sessionID]
	w.mu.Unlock()
	if h == nil {
		return fmt.Errorf("no active chat for session %q", sessionID)
	}
	switch strings.ToLower(req.Type) {
	case "exec":
		if req.ConfirmID == "" {
			return fmt.Errorf("confirm_id required")
		}
		return h.resolveExec(req.ConfirmID, req.Approved)
	case "choice":
		if req.ChoiceID == "" {
			return fmt.Errorf("choice_id required")
		}
		return h.resolveChoice(req.ChoiceID, req.Selected)
	default:
		return fmt.Errorf("unknown resolve type %q (want exec|choice)", req.Type)
	}
}

// Reset 清空会话（断开连接，下次重建）。
func (w *Worker) Reset(sessionID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if c, ok := w.conns[sessionID]; ok {
		_ = c.Close()
		delete(w.conns, sessionID)
	}
	delete(w.handlers, sessionID)
	return nil
}

// SessionCount 当前活跃会话数。
func (w *Worker) SessionCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.conns)
}

// CloseAll 关闭全部连接。
func (w *Worker) CloseAll() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for k, c := range w.conns {
		_ = c.Close()
		delete(w.conns, k)
	}
	w.handlers = make(map[string]*httpStreamHandler)
}

// resolveExec 回写 exec 确认。
func (h *httpStreamHandler) resolveExec(confirmID string, approved bool) error {
	h.mu.Lock()
	ch := h.pending[confirmID]
	delete(h.pending, confirmID)
	h.mu.Unlock()
	if ch == nil {
		return fmt.Errorf("unknown confirm_id %q", confirmID)
	}
	select {
	case ch <- approved:
		return nil
	default:
		return fmt.Errorf("confirm already resolved")
	}
}

// resolveChoice 回写选择。
func (h *httpStreamHandler) resolveChoice(choiceID string, selected []string) error {
	key := "choice:" + choiceID
	h.mu.Lock()
	ch := h.pick[key]
	delete(h.pick, key)
	h.mu.Unlock()
	if ch == nil {
		return fmt.Errorf("unknown choice_id %q", choiceID)
	}
	select {
	case ch <- selected:
		return nil
	default:
		return fmt.Errorf("choice already resolved")
	}
}