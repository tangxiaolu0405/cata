// Package api 提供模式二 HTTP API（cata serve-api）：
// 复用 socket_chat 语义（NDJSON 事件流），通过 HTTP/SSE 暴露给远程客户端。
//
// 架构：
//   - 每个会话（X-Session-Id / session_id）复用一条 socketclient.Conn（保留 server 侧 history）
//   - POST /v1/chat 以 SSE 流式下发 token/progress/tool_start/exec_confirm_required/user_choice/done
//   - exec_confirm_required / user_choice 事件挂起等待，由 POST /v1/resolve 回写
//   - 认证：Authorization: Bearer <token>（--token / CATA_API_TOKEN / config api.token）
//
// 与 gateway 的渠道（telegram/qq）共用同一 socket 协议；本包是「HTTP 即渠道」的独立入口。
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"cata/internal/cata/socketclient"
)

// DefaultListen 默认监听地址（仅本机回环；公网暴露需显式 --listen 0.0.0.0:port 并配 token）。
const DefaultListen = "127.0.0.1:8788"

// Server HTTP API 服务。
type Server struct {
	token    string // 空 = 仅本机回环可访问（不校验）；非空 = 强制 Bearer
	listen   string
	worker   *Worker // 与 socket server 的桥接
	httpSrv  *http.Server
	started  chan struct{}
	stopOnce sync.Once
}

// NewServer 创建 HTTP API 服务。token 为空时仅允许本机回环。
func NewServer(listen, token string, worker *Worker) *Server {
	if strings.TrimSpace(listen) == "" {
		listen = DefaultListen
	}
	return &Server{
		token:   strings.TrimSpace(token),
		listen:  listen,
		worker:  worker,
		started: make(chan struct{}),
	}
}

// ListenAddr 返回实际监听地址（启动后可用）。
func (s *Server) ListenAddr() string {
	return s.listen
}

// Start 启动 HTTP 服务（阻塞直到 ctx 取消或服务出错）。
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat", s.handleChat)
	mux.HandleFunc("/v1/resolve", s.handleResolve)
	mux.HandleFunc("/v1/status", s.handleStatus)
	mux.HandleFunc("/v1/reset", s.handleReset)

	// 认证中间件：token 为空时仅允许本机回环；非空时校验 Bearer。
	handler := s.authMiddleware(mux)

	ln, err := net.Listen("tcp", s.listen)
	if err != nil {
		return fmt.Errorf("api listen %s: %w", s.listen, err)
	}
	s.httpSrv = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	close(s.started)

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.httpSrv.Serve(ln)
	}()
	log.Printf("cata serve-api: listening on %s (token=%t)", ln.Addr().String(), s.token != "")

	select {
	case <-ctx.Done():
		shCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(shCtx)
		return ctx.Err()
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

// Stop 停止 HTTP 服务（幂等）。
func (s *Server) Stop() {
	s.stopOnce.Do(func() {
		if s.httpSrv != nil {
			shCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = s.httpSrv.Shutdown(shCtx)
		}
	})
}

// authMiddleware 认证：token 为空 → 仅本机回环；非空 → Bearer 校验（常量时间比较）。
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			// 无 token：仅允许本机回环，防公网误暴露。
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil || !isLoopback(host) {
				http.Error(w, `{"error":"token required"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		got := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopback(host string) bool {
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}

// chatRequest POST /v1/chat 请求体。
type chatRequest struct {
	// Text 用户消息。
	Text string `json:"text"`
	// SessionID 会话标识（复用连接保留 history）；空 = 默认会话。
	SessionID string `json:"session_id,omitempty"`
	// Cwd 产出区（工作空间根路径）；空 = worker 默认目录。
	Cwd string `json:"cwd,omitempty"`
	// Attachments 附件（path 相对产出区 / inline base64）。
	Attachments []socketclient.AttachmentReq `json:"attachments,omitempty"`
	// RunAs 会话类型（"" 普通；"scheduled" 定时任务）。
	RunAs string `json:"run_as,omitempty"`
	// ShowThinking 是否下发 thinking 事件。
	ShowThinking bool `json:"show_thinking,omitempty"`
}

// handleChat 处理 POST /v1/chat：以 SSE 流式返回 NDJSON 事件。
// 请求体为 JSON；响应 Content-Type: text/event-stream（data: <json>\n\n）。
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"bad request: %v"}`, err), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		http.Error(w, `{"error":"text required"}`, http.StatusBadRequest)
		return
	}

	// 会话键：session_id 或默认。
	sessID := strings.TrimSpace(req.SessionID)
	if sessID == "" {
		sessID = "default"
	}

	// 建立 SSE 响应。
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming unsupported"}`, http.StatusInternalServerError)
		return
	}

	// emit 写 SSE 事件。
	emit := func(ev map[string]any) error {
		data, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	conn, err := s.worker.Conn(r.Context(), sessID, req.Cwd)
	if err != nil {
		_ = emit(map[string]any{"type": "error", "message": err.Error()})
		return
	}

	h := &httpStreamHandler{
		emit:    emit,
		worker:  s.worker,
		sessID:  sessID,
		ctx:     r.Context(),
		mu:      &sync.Mutex{},
		pending: make(map[string]chan bool),
		pick:    make(map[string]chan []string),
	}
	// 交互回写挂到 worker（/v1/resolve 按 confirm_id/choice_id 找到会话）。
	s.worker.attachHandler(sessID, h)
	defer s.worker.detachHandler(sessID)

	result, err := conn.ChatAsWithAttachments(r.Context(), req.Text, req.RunAs, req.Attachments, h)
	if err != nil {
		// 流式错误：已发 error 事件（或 ctx 取消）。
		if r.Context().Err() == nil {
			_ = emit(map[string]any{"type": "error", "message": err.Error()})
		}
		return
	}
	_ = emit(map[string]any{
		"type":     "done",
		"success":  result.Success,
		"cancelled": result.Cancelled,
		"err_msg":  result.ErrMsg,
	})
}

// resolveRequest POST /v1/resolve 请求体。
type resolveRequest struct {
	// Type 回写类型："exec" | "choice"。
	Type string `json:"type"`
	// ConfirmID exec 确认 id（type=exec）。
	ConfirmID string `json:"confirm_id,omitempty"`
	// Approved exec 是否批准（type=exec）。
	Approved bool `json:"approved,omitempty"`
	// ChoiceID 选择 id（type=choice）。
	ChoiceID string `json:"choice_id,omitempty"`
	// Selected 选中项（type=choice）。
	Selected []string `json:"selected,omitempty"`
	// SessionID 会话 id（定位挂起的 handler；空 = 默认会话）。
	SessionID string `json:"session_id,omitempty"`
}

// handleResolve 处理 POST /v1/resolve：回写 exec_confirm / user_choice。
func (s *Server) handleResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req resolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"bad request: %v"}`, err), http.StatusBadRequest)
		return
	}
	sessID := strings.TrimSpace(req.SessionID)
	if sessID == "" {
		sessID = "default"
	}
	err := s.worker.Resolve(sessID, req)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"success":true}`))
}

// handleStatus GET /v1/status：健康检查 + 会话数。
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":       true,
		"sessions": s.worker.SessionCount(),
	})
}

// handleReset POST /v1/reset：清空指定会话（或全部）。
func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		SessionID string `json:"session_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"bad request: %v"}`, err), http.StatusBadRequest)
		return
	}
	sessID := strings.TrimSpace(req.SessionID)
	if sessID == "" {
		sessID = "default"
	}
	if err := s.worker.Reset(sessID); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"success":true}`))
}

// httpStreamHandler 实现 socketclient.StreamHandler：把 socket 事件 emit 到 SSE，
// 交互（exec_confirm/user_choice）挂起等待 /v1/resolve 回写。
type httpStreamHandler struct {
	emit    func(map[string]any) error
	worker  *Worker
	sessID  string
	ctx     context.Context
	mu      *sync.Mutex
	pending map[string]chan bool   // confirm_id → 等待通道（exec）
	pick    map[string]chan []string // "choice:"+id → 等待通道
}

func (h *httpStreamHandler) OnToken(s string) {
	_ = h.emit(map[string]any{"type": "token", "content": s})
}

func (h *httpStreamHandler) OnProgress(message string) {
	_ = h.emit(map[string]any{"type": "progress", "message": message})
}

func (h *httpStreamHandler) OnToolStart(name string) {
	_ = h.emit(map[string]any{"type": "tool_start", "name": name})
}

func (h *httpStreamHandler) ConfirmExec(ctx context.Context, p socketclient.ExecConfirmPrompt) (bool, error) {
	ch := make(chan bool, 1)
	h.mu.Lock()
	h.pending[p.ConfirmID] = ch
	h.mu.Unlock()
	_ = h.emit(map[string]any{
		"type":         "exec_confirm_required",
		"confirm_id":   p.ConfirmID,
		"command_line": p.CommandLine,
		"cwd":          p.Cwd,
	})
	select {
	case <-ctx.Done():
		h.mu.Lock()
		delete(h.pending, p.ConfirmID)
		h.mu.Unlock()
		return false, ctx.Err()
	case ok := <-ch:
		return ok, nil
	case <-time.After(10 * time.Minute):
		h.mu.Lock()
		delete(h.pending, p.ConfirmID)
		h.mu.Unlock()
		return false, fmt.Errorf("exec confirm timeout")
	}
}

func (h *httpStreamHandler) Choose(ctx context.Context, p socketclient.UserChoicePrompt) ([]string, error) {
	// 选择挂起：复用 pending（choice_id 前缀 choice:，避免与 confirm 冲突）。
	ch := make(chan []string, 1)
	key := "choice:" + p.ChoiceID
	h.mu.Lock()
	h.pick[key] = ch
	h.mu.Unlock()
	opts := make([]map[string]string, 0, len(p.Options))
	for _, o := range p.Options {
		opts = append(opts, map[string]string{"id": o.ID, "label": o.Label, "desc": o.Desc})
	}
	_ = h.emit(map[string]any{
		"type":    "user_choice",
		"id":      p.ChoiceID,
		"prompt":  p.Prompt,
		"multi":   p.Multi,
		"options": opts,
	})
	select {
	case <-ctx.Done():
		h.mu.Lock()
		delete(h.pick, key)
		h.mu.Unlock()
		return nil, ctx.Err()
	case sel := <-ch:
		return sel, nil
	case <-time.After(10 * time.Minute):
		h.mu.Lock()
		delete(h.pick, key)
		h.mu.Unlock()
		return nil, fmt.Errorf("user choice timeout")
	}
}