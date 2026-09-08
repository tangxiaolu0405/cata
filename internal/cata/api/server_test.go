package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cata/internal/cata/socketclient"
)

// pipeDialer 返回 net.Pipe 的一端作为 dialer；另一端由测试扮演 socket server。
func pipeDialer(t *testing.T) (serverEnd net.Conn, dial func() (net.Conn, error)) {
	t.Helper()
	c1, c2 := net.Pipe()
	return c1, func() (net.Conn, error) { return c2, nil }
}

// mockSocketServer 扮演 cata socket server：读 chat 请求，按脚本发 NDJSON 事件。
type mockSocketServer struct {
	conn net.Conn
	br   *bufio.Reader
	t    *testing.T
}

func newMockServer(t *testing.T, conn net.Conn) *mockSocketServer {
	return &mockSocketServer{conn: conn, br: bufio.NewReader(conn), t: t}
}

// readRequest 读一条客户端请求（chat / exec_confirm / user_choice）。
func (m *mockSocketServer) readRequest() map[string]any {
	m.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := m.br.ReadBytes('\n')
	if err != nil {
		m.t.Fatalf("mock server read request: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(line), &req); err != nil {
		m.t.Fatalf("mock server parse request: %v (line=%s)", err, line)
	}
	return req
}

func (m *mockSocketServer) writeEvent(ev map[string]any) {
	data, _ := json.Marshal(ev)
	data = append(data, '\n')
	m.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := m.conn.Write(data); err != nil {
		m.t.Fatalf("mock server write event: %v", err)
	}
}

// newTestServer 构建一个用 pipe 拨号的 API Server + Worker。
// dial 返回 worker 侧连接（pipe 的 client 端）；mock 用 server 端（serverEnd）。
func newTestServer(t *testing.T, token string, dial func() (net.Conn, error)) (*Server, *Worker, *http.Handler) {
	t.Helper()
	worker := NewWorker("test-ws", "/tmp")
	worker.Dial = dial
	srv := NewServer("127.0.0.1:0", token, worker)
	// 用 httptest 起 handler（不真正监听，直接测 handler 逻辑）。
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat", srv.handleChat)
	mux.HandleFunc("/v1/resolve", srv.handleResolve)
	mux.HandleFunc("/v1/status", srv.handleStatus)
	mux.HandleFunc("/v1/reset", srv.handleReset)
	h := srv.authMiddleware(mux)
	return srv, worker, &h
}

// TestAuthNoTokenLoopbackOnly 无 token 时仅本机回环可访问。
func TestAuthNoTokenLoopbackOnly(t *testing.T) {
	serverEnd, dial := pipeDialer(t)
	defer serverEnd.Close()
	srv, _, h := newTestServer(t, "", dial)

	// 本机回环：允许（但 chat 请求会因 mock server 未读而卡住，这里只测认证层用 status）。
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	(*h).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("loopback status: got %d want 200", rr.Code)
	}

	// 非本机：401。
	req2 := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req2.RemoteAddr = "192.168.1.10:12345"
	rr2 := httptest.NewRecorder()
	(*h).ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("non-loopback status: got %d want 401", rr2.Code)
	}
	_ = srv
}

// TestAuthBearerToken 有 token 时 Bearer 校验。
func TestAuthBearerToken(t *testing.T) {
	serverEnd, dial := pipeDialer(t)
	defer serverEnd.Close()
	_, _, h := newTestServer(t, "secret-token", dial)

	// 无 Authorization：401。
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.RemoteAddr = "192.168.1.10:12345"
	rr := httptest.NewRecorder()
	(*h).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no auth: got %d want 401", rr.Code)
	}

	// 错误 token：401。
	req2 := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req2.RemoteAddr = "192.168.1.10:12345"
	req2.Header.Set("Authorization", "Bearer wrong")
	rr2 := httptest.NewRecorder()
	(*h).ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: got %d want 401", rr2.Code)
	}

	// 正确 token：200。
	req3 := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req3.RemoteAddr = "192.168.1.10:12345"
	req3.Header.Set("Authorization", "Bearer secret-token")
	rr3 := httptest.NewRecorder()
	(*h).ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Fatalf("correct token: got %d want 200", rr3.Code)
	}
}

// TestChatStreaming 验证 chat 的 SSE 流式事件（token → done）。
func TestChatStreaming(t *testing.T) {
	serverEnd, dial := pipeDialer(t)
	defer serverEnd.Close()
	mock := newMockServer(t, serverEnd)
	_, _, h := newTestServer(t, "", dial)

	// 后台：扮演 socket server，读 chat 请求后发 token + done。
	go func() {
		req := mock.readRequest()
		if req["command"] != "chat" {
			mock.t.Errorf("expected chat command, got %v", req["command"])
			return
		}
		mock.writeEvent(map[string]any{"type": "token", "content": "Hello"})
		mock.writeEvent(map[string]any{"type": "token", "content": " world"})
		mock.writeEvent(map[string]any{"type": "done", "success": true, "cancelled": false})
	}()

	body := strings.NewReader(`{"text":"hi","session_id":"s1"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat", body)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	(*h).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("chat: got %d want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type: got %q want text/event-stream", ct)
	}
	out := rr.Body.String()
	if !strings.Contains(out, `"type":"token"`) || !strings.Contains(out, `"content":"Hello"`) {
		t.Fatalf("missing token event: %s", out)
	}
	if !strings.Contains(out, `"type":"done"`) || !strings.Contains(out, `"success":true`) {
		t.Fatalf("missing done event: %s", out)
	}
}

// TestChatReusesSession 会话复用：第二次 chat 复用同一连接（server 侧 history 保留）。
func TestChatReusesSession(t *testing.T) {
	serverEnd, dial := pipeDialer(t)
	defer serverEnd.Close()
	mock := newMockServer(t, serverEnd)
	_, worker, h := newTestServer(t, "", dial)

	chatOnce := func(text string) {
		body := strings.NewReader(`{"text":"` + text + `","session_id":"s1"}`)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat", body)
		req.RemoteAddr = "127.0.0.1:12345"
		rr := httptest.NewRecorder()
		(*h).ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("chat %s: got %d", text, rr.Code)
		}
	}

	// 第一次 chat：server 读请求、发 done。
	go func() {
		mock.readRequest()
		mock.writeEvent(map[string]any{"type": "done", "success": true})
	}()
	chatOnce("first")

	// 第二次 chat：应复用同一连接（worker.conns 里 s1 已存在且 healthy）。
	conn := worker.conn("s1")
	if conn == nil {
		t.Fatal("expected session conn to be cached")
	}
	// 第二次 chat 在同一连接上：mock server 继续读。
	go func() {
		req := mock.readRequest()
		if req["text"] != "second" {
			mock.t.Errorf("second chat text: got %v want second", req["text"])
		}
		mock.writeEvent(map[string]any{"type": "done", "success": true})
	}()
	chatOnce("second")
}

// TestResolveExec 验证 exec_confirm_required 挂起 + /v1/resolve 回写。
func TestResolveExec(t *testing.T) {
	serverEnd, dial := pipeDialer(t)
	defer serverEnd.Close()
	mock := newMockServer(t, serverEnd)
	_, _, h := newTestServer(t, "", dial)

	// 后台：server 读 chat 请求，发 exec_confirm_required；等客户端回 exec_confirm。
	go func() {
		mock.readRequest() // chat
		mock.writeEvent(map[string]any{
			"type": "exec_confirm_required", "confirm_id": "c1",
			"command_line": "rm -rf /tmp/x", "cwd": "/tmp",
		})
		// 等客户端回写 exec_confirm。
		req := mock.readRequest()
		if req["command"] != "exec_confirm" {
			mock.t.Errorf("expected exec_confirm, got %v", req["command"])
			return
		}
		if req["confirm_id"] != "c1" || req["approved"] != true {
			mock.t.Errorf("exec_confirm payload mismatch: %v", req)
			return
		}
		mock.writeEvent(map[string]any{"type": "done", "success": true})
	}()

	// chat 请求在 goroutine 中跑（会阻塞等 resolve）。
	body := strings.NewReader(`{"text":"run it","session_id":"s1"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat", body)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		(*h).ServeHTTP(rr, req)
		close(done)
	}()

	// 等 mock server 发出 exec_confirm_required（事件先到 SSE）。
	// 轮询 status 里 pending 挂起：直接小睡等 server 读到 chat 请求。
	time.Sleep(200 * time.Millisecond)

	// 回写 resolve。
	resBody := strings.NewReader(`{"type":"exec","confirm_id":"c1","approved":true,"session_id":"s1"}`)
	resReq := httptest.NewRequest(http.MethodPost, "/v1/resolve", resBody)
	resReq.RemoteAddr = "127.0.0.1:12345"
	resRR := httptest.NewRecorder()
	(*h).ServeHTTP(resRR, resReq)
	if resRR.Code != http.StatusOK {
		t.Fatalf("resolve: got %d want 200 (body=%s)", resRR.Code, resRR.Body.String())
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("chat did not finish after resolve")
	}
	out := rr.Body.String()
	if !strings.Contains(out, `"type":"exec_confirm_required"`) {
		t.Fatalf("missing exec_confirm_required event: %s", out)
	}
}

// TestResolveChoice 验证 user_choice 挂起 + /v1/resolve 回写。
func TestResolveChoice(t *testing.T) {
	serverEnd, dial := pipeDialer(t)
	defer serverEnd.Close()
	mock := newMockServer(t, serverEnd)
	_, _, h := newTestServer(t, "", dial)

	go func() {
		mock.readRequest() // chat
		mock.writeEvent(map[string]any{
			"type": "user_choice", "id": "ch1", "prompt": "pick one", "multi": false,
			"options": []map[string]string{{"id": "a", "label": "A"}, {"id": "b", "label": "B"}},
		})
		req := mock.readRequest()
		if req["command"] != "user_choice" {
			mock.t.Errorf("expected user_choice, got %v", req["command"])
			return
		}
		if req["choice_id"] != "ch1" {
			mock.t.Errorf("user_choice payload mismatch: %v", req)
			return
		}
		mock.writeEvent(map[string]any{"type": "done", "success": true})
	}()

	body := strings.NewReader(`{"text":"choose","session_id":"s1"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat", body)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		(*h).ServeHTTP(rr, req)
		close(done)
	}()

	time.Sleep(200 * time.Millisecond)

	resBody := strings.NewReader(`{"type":"choice","choice_id":"ch1","selected":["a"],"session_id":"s1"}`)
	resReq := httptest.NewRequest(http.MethodPost, "/v1/resolve", resBody)
	resReq.RemoteAddr = "127.0.0.1:12345"
	resRR := httptest.NewRecorder()
	(*h).ServeHTTP(resRR, resReq)
	if resRR.Code != http.StatusOK {
		t.Fatalf("resolve choice: got %d want 200 (body=%s)", resRR.Code, resRR.Body.String())
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("chat did not finish after choice resolve")
	}
	if !strings.Contains(rr.Body.String(), `"type":"user_choice"`) {
		t.Fatalf("missing user_choice event: %s", rr.Body.String())
	}
}

// TestResetDropsSession 验证 /v1/reset 断开会话连接。
func TestResetDropsSession(t *testing.T) {
	serverEnd, dial := pipeDialer(t)
	defer serverEnd.Close()
	mock := newMockServer(t, serverEnd)
	_, worker, h := newTestServer(t, "", dial)

	// 建立会话连接。
	go func() {
		mock.readRequest()
		mock.writeEvent(map[string]any{"type": "done", "success": true})
	}()
	body := strings.NewReader(`{"text":"hi","session_id":"s1"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat", body)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	(*h).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("chat: got %d", rr.Code)
	}
	if worker.SessionCount() != 1 {
		t.Fatalf("session count: got %d want 1", worker.SessionCount())
	}

	// reset。
	resBody := strings.NewReader(`{"session_id":"s1"}`)
	resReq := httptest.NewRequest(http.MethodPost, "/v1/reset", resBody)
	resReq.RemoteAddr = "127.0.0.1:12345"
	resRR := httptest.NewRecorder()
	(*h).ServeHTTP(resRR, resReq)
	if resRR.Code != http.StatusOK {
		t.Fatalf("reset: got %d want 200", resRR.Code)
	}
	if worker.SessionCount() != 0 {
		t.Fatalf("session count after reset: got %d want 0", worker.SessionCount())
	}
}

// conn 辅助：worker 内部取连接（测试用）。
func (w *Worker) conn(sessionID string) *socketclient.Conn {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conns[sessionID]
}