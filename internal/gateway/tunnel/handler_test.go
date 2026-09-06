package tunnel

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"cata/internal/cata/tunnel"
)

func newTestHandlerServer(t *testing.T, opts HandlerOptions) (*httptest.Server, *Registry, *MachinesStore) {
	t.Helper()
	reg := NewRegistry()
	if opts.Machines == nil {
		opts.Machines = NewMachinesStore(filepath.Join(t.TempDir(), "machines.json"))
	}
	srv := httptest.NewServer(Handler(reg, opts))
	return srv, reg, opts.Machines
}

func wsURL(srv *httptest.Server, agent string) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/?agent=" + agent
}

func TestHandlerHTTPLayerAuth(t *testing.T) {
	srv, _, _ := newTestHandlerServer(t, HandlerOptions{Token: "secret"})
	defer srv.Close()

	// 非 websocket Upgrade → 400。
	resp, err := http.Get(srv.URL + "?agent=a")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("non-websocket: want 400, got %d", resp.StatusCode)
	}

	// 缺 agent 参数 → 400。
	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Version", "13")
	resp2, _ := http.DefaultClient.Do(req)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing agent: want 400, got %d", resp2.StatusCode)
	}

	// 白名单外 → 403。
	srv2, _, _ := newTestHandlerServer(t, HandlerOptions{Token: "secret", AllowAgentIDs: []string{"allowed"}})
	defer srv2.Close()
	req4, _ := http.NewRequest("GET", srv2.URL+"?agent=other", nil)
	req4.Header.Set("Upgrade", "websocket")
	req4.Header.Set("Authorization", "Bearer secret")
	resp4, _ := http.DefaultClient.Do(req4)
	resp4.Body.Close()
	if resp4.StatusCode != http.StatusForbidden {
		t.Fatalf("not allowed: want 403, got %d", resp4.StatusCode)
	}
}

func TestHandlerHelloMachineAuth(t *testing.T) {
	srv, _, store := newTestHandlerServer(t, HandlerOptions{Token: "secret", Machines: newStore(t)})
	defer srv.Close()
	goodToken, _ := store.IssueToken("machine-1")

	// 错误 machine token → FrameError。
	conn := dialWS(t, srv, "agent-1", "secret")
	defer conn.Close()
	_ = conn.WriteJSON(tunnel.Frame{Type: tunnel.FrameHello, AgentID: "agent-1",
		MachineID: "machine-1", MachineToken: "wrong", Protocol: tunnel.ProtocolName, Version: tunnel.Version})
	var errFrame tunnel.Frame
	if err := conn.ReadJSON(&errFrame); err != nil {
		t.Fatal(err)
	}
	if errFrame.Type != tunnel.FrameError {
		t.Fatalf("bad machine token: want FrameError, got %q (%q)", errFrame.Type, errFrame.Message)
	}
	conn.Close()

	// 正确 machine token、无 agent_token：首次注册 → 网关下发 hello_ack（含 per-agent token）并关闭，
	// 要求 worker 带 agent_token 重连（见 TestHandlerAgentTokenBootstrap）。
	conn2 := dialWS(t, srv, "agent-1", "secret")
	defer conn2.Close()
	_ = conn2.WriteJSON(tunnel.Frame{Type: tunnel.FrameHello, AgentID: "agent-1",
		MachineID: "machine-1", MachineToken: goodToken, Protocol: tunnel.ProtocolName, Version: tunnel.Version})
	var ack tunnel.Frame
	if err := conn2.ReadJSON(&ack); err != nil {
		t.Fatal(err)
	}
	if ack.Type != tunnel.FrameHelloAck {
		t.Fatalf("first hello: want hello_ack, got %q (%q)", ack.Type, ack.Message)
	}
	if !store.ValidateAgent("agent-1", ack.AgentToken) {
		t.Fatal("hello_ack agent token should be the issued one")
	}
}

// TestHandlerAgentTokenBootstrap 端到端 per-agent token 引导：
// ① machine token 首次注册 → hello_ack 拿 token（连接被关）；
// ② 带 agent_token 重连 → 注册成功 online。
func TestHandlerAgentTokenBootstrap(t *testing.T) {
	srv, reg, store := newTestHandlerServer(t, HandlerOptions{Token: "secret", Machines: newStore(t)})
	defer srv.Close()
	machineTok, _ := store.IssueToken("machine-1")

	// ① 首次：machine token 触发签发，hello_ack 返回 token。
	conn1 := dialWS(t, srv, "ws-a", "secret")
	_ = conn1.WriteJSON(tunnel.Frame{Type: tunnel.FrameHello, AgentID: "ws-a",
		MachineID: "machine-1", MachineToken: machineTok, Protocol: tunnel.ProtocolName, Version: tunnel.Version})
	var ack tunnel.Frame
	if err := conn1.ReadJSON(&ack); err != nil {
		t.Fatal(err)
	}
	if ack.Type != tunnel.FrameHelloAck || ack.AgentToken == "" {
		t.Fatalf("want hello_ack with token, got %+v", ack)
	}
	conn1.Close() // 网关已关

	// ② 带 agent_token 重连 → 在线。
	conn2 := dialWS(t, srv, "ws-a", "secret")
	defer conn2.Close()
	_ = conn2.WriteJSON(tunnel.Frame{Type: tunnel.FrameHello, AgentID: "ws-a",
		MachineID: "machine-1", MachineToken: machineTok, AgentToken: ack.AgentToken,
		Protocol: tunnel.ProtocolName, Version: tunnel.Version})
	waitForOnline(t, reg, "ws-a")

	// 已存在 token 但 worker 未带 agent_token → 拒绝并提示（防 machine token 无限续期）。
	conn3 := dialWS(t, srv, "ws-a", "secret")
	defer conn3.Close()
	_ = conn3.WriteJSON(tunnel.Frame{Type: tunnel.FrameHello, AgentID: "ws-a",
		MachineID: "machine-1", MachineToken: machineTok, Protocol: tunnel.ProtocolName, Version: tunnel.Version})
	var errF tunnel.Frame
	if err := conn3.ReadJSON(&errF); err != nil {
		t.Fatal(err)
	}
	if errF.Type != tunnel.FrameError || !strings.Contains(errF.Message, "agent_token") {
		t.Fatalf("existing token without agent_token: want error containing agent_token, got %+v", errF)
	}

	// 错误 agent_token → 拒绝。
	conn4 := dialWS(t, srv, "ws-a", "secret")
	defer conn4.Close()
	_ = conn4.WriteJSON(tunnel.Frame{Type: tunnel.FrameHello, AgentID: "ws-a",
		MachineID: "machine-1", MachineToken: machineTok, AgentToken: "wrong",
		Protocol: tunnel.ProtocolName, Version: tunnel.Version})
	var errF2 tunnel.Frame
	if err := conn4.ReadJSON(&errF2); err != nil {
		t.Fatal(err)
	}
	if errF2.Type != tunnel.FrameError {
		t.Fatalf("bad agent token: want FrameError, got %q", errF2.Type)
	}
}

func dialWS(t *testing.T, srv *httptest.Server, agent, token string) *websocket.Conn {
	t.Helper()
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL(srv, agent), hdr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn
}

func waitForOnline(t *testing.T, reg *Registry, agentID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, a := range reg.OnlineAgents() {
			if a.AgentID == agentID {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("agent %s did not come online", agentID)
}

func newStore(t *testing.T) *MachinesStore {
	t.Helper()
	return NewMachinesStore(filepath.Join(t.TempDir(), "machines.json"))
}
