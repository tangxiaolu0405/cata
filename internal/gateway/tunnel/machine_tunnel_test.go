package tunnel

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"cata/internal/cata/tunnel"
)

// startEchoUnix 起一个回显 "echo:<line>" 的本地 Unix socket（模拟某工作空间的 per-ws socket）。
func startEchoUnix(t *testing.T, sockPath string) {
	t.Helper()
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				for {
					line, err := br.ReadBytes('\n')
					if err != nil {
						return
					}
					_, _ = c.Write(append([]byte("echo:"), line...))
				}
			}(c)
		}
	}()
}

// fakeHub 模拟 supervisor 侧的机器级隧道 worker：open 帧里带目标 agent，拨本地 socket 并转发。
type fakeHub struct {
	ws       *websocket.Conn
	sockPath string

	wmu sync.Mutex
	mu  sync.Mutex
	// opened 记录每次 open 帧要求的目标工作空间（网关应带上 AgentID 提示）。
	opened []string
	// revoked 记录网关下发的 revoke 帧目标。
	revoked []string
}

func (h *fakeHub) send(f tunnel.Frame) error {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	return h.ws.WriteJSON(f)
}

func (h *fakeHub) record(agentID string) {
	h.mu.Lock()
	h.opened = append(h.opened, agentID)
	h.mu.Unlock()
}

func (h *fakeHub) openedIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.opened))
	copy(out, h.opened)
	return out
}

func (h *fakeHub) revokedIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.revoked))
	copy(out, h.revoked)
	return out
}

func (h *fakeHub) serve() {
	streams := map[uint64]net.Conn{}
	for {
		_, msg, err := h.ws.ReadMessage()
		if err != nil {
			return
		}
		var f tunnel.Frame
		if err := json.Unmarshal(msg, &f); err != nil {
			continue
		}
		switch f.Type {
		case tunnel.FrameOpen:
			h.record(f.AgentID)
			c, err := net.Dial("unix", h.sockPath)
			if err != nil {
				_ = h.send(tunnel.Frame{Type: tunnel.FrameError, Stream: f.Stream, Message: err.Error()})
				_ = h.send(tunnel.Frame{Type: tunnel.FrameClose, Stream: f.Stream})
				continue
			}
			streams[f.Stream] = c
			_ = h.send(tunnel.Frame{Type: tunnel.FrameOpened, Stream: f.Stream})
			go func(id uint64, c net.Conn) {
				br := bufio.NewReader(c)
				for {
					line, err := br.ReadBytes('\n')
					if err != nil {
						_ = h.send(tunnel.Frame{Type: tunnel.FrameClose, Stream: id})
						_ = c.Close()
						return
					}
					if err := h.send(tunnel.Frame{Type: tunnel.FrameLine, Stream: id, Data: tunnel.EncodeData(line)}); err != nil {
						_ = c.Close()
						return
					}
				}
			}(f.Stream, c)
		case tunnel.FrameLine:
			data, err := tunnel.DecodeData(f.Data)
			if err != nil {
				continue
			}
			if c, ok := streams[f.Stream]; ok {
				_, _ = c.Write(data)
			}
		case tunnel.FrameClose:
			if c, ok := streams[f.Stream]; ok {
				_ = c.Close()
				delete(streams, f.Stream)
			}
		case tunnel.FrameRevoke:
			h.mu.Lock()
			h.revoked = append(h.revoked, f.AgentID)
			h.mu.Unlock()
		}
	}
}

// newMachineTunnelTestEnv 起网关 + 一台机器的机器级隧道（hello 带工作空间清单）。
// 返回 registry、隧道连接、模拟 worker。
func newMachineTunnelTestEnv(t *testing.T) (*Registry, *websocket.Conn, *fakeHub) {
	t.Helper()
	// Unix socket 路径有长度上限（macOS ~104B）：临时目录名要短，不能直接用 t.TempDir()。
	dir, err := os.MkdirTemp("", "ct")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sockPath := filepath.Join(dir, "ws-9.sock")
	startEchoUnix(t, sockPath)

	store := NewMachinesStore(filepath.Join(dir, "machines.json"))
	token, err := store.IssueToken("m1")
	if err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry()
	ts := httptest.NewServer(Handler(reg, HandlerOptions{Machines: store}))
	t.Cleanup(ts.Close)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/cata/v1/tunnel?machine=m1"
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+token)
	ws, resp, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		t.Fatalf("dial machine tunnel: %v (resp=%v)", err, resp)
	}
	t.Cleanup(func() { _ = ws.Close() })

	hello := tunnel.Frame{
		Type:          tunnel.FrameHello,
		MachineTunnel: true,
		MachineID:     "m1",
		MachineToken:  token,
		Name:          "host-a",
		Protocol:      tunnel.ProtocolName,
		Version:       tunnel.Version,
		Agents: []tunnel.AgentEntry{
			{AgentID: "ws-9", Name: "proj-9", RootPath: "/p/9"},
			{AgentID: "blocked", Name: "blocked", RootPath: "/p/b"},
		},
	}
	if err := ws.WriteJSON(hello); err != nil {
		t.Fatal(err)
	}
	hub := &fakeHub{ws: ws, sockPath: sockPath}
	waitUntil(t, 5*time.Second, func() bool { return reg.AgentAlive("ws-9") })
	go hub.serve()
	return reg, ws, hub
}

// TestMachineTunnelRoutesToSleepingAgent 核心链路：机器级隧道发布的工作空间（agent 进程
// 当前不在线 = Sleeping）仍可路由——网关下发 open{AgentID}，supervisor 侧拉起/拨号后
// 字节双向透传。
func TestMachineTunnelRoutesToSleepingAgent(t *testing.T) {
	reg, _, hub := newMachineTunnelTestEnv(t)

	// 休眠工作空间应出现在可路由列表里（UI 项目列表据此仍能看到它）。
	var found *AgentInfo
	for _, a := range reg.OnlineAgents() {
		if a.AgentID == "ws-9" {
			cp := a
			found = &cp
		}
	}
	if found == nil {
		t.Fatal("hub-published workspace missing from OnlineAgents")
	}
	if !found.Sleeping {
		t.Fatal("hub-published workspace should be marked Sleeping")
	}
	if found.MachineID != "m1" || found.RootPath != "/p/9" {
		t.Fatalf("hub entry info mismatch: %+v", *found)
	}
	if info, ok := reg.FindAgent("ws-9"); !ok || info.Name != "proj-9" {
		t.Fatalf("FindAgent(ws-9) = %+v ok=%v", info, ok)
	}

	conn, err := reg.DialAgent("ws-9")
	if err != nil {
		t.Fatalf("dial sleeping workspace: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("hi\n")); err != nil {
		t.Fatal(err)
	}
	if got := readWithTimeout(t, conn, make([]byte, 64)); got != "echo:hi\n" {
		t.Fatalf("roundtrip got %q, want %q", got, "echo:hi\n")
	}
	// 网关必须把目标工作空间写进 open 帧（机器级隧道需要它决定拉起哪个 agent）。
	ids := hub.openedIDs()
	if len(ids) != 1 || ids[0] != "ws-9" {
		t.Fatalf("open frames targeted %v, want [ws-9]", ids)
	}

	// 未注册/未被任何隧道服务的工作空间：路由应报错，而不是静默失败。
	if _, err := reg.DialAgent("nope"); err == nil {
		t.Fatal("dial unserved workspace should fail")
	}
}

// TestMachineTunnelDisconnectStopsRouting 隧道断开后，其发布的工作空间不再可路由。
func TestMachineTunnelDisconnectStopsRouting(t *testing.T) {
	reg, ws, _ := newMachineTunnelTestEnv(t)
	_ = ws.Close()
	waitUntil(t, 5*time.Second, func() bool { return !reg.AgentAlive("ws-9") })
	if _, err := reg.DialAgent("ws-9"); err == nil {
		t.Fatal("dial after tunnel closed should fail")
	}
}

// TestMachineTunnelAgentsUpdate 隧道在线期间推送 agents 帧：清单即时生效（新增/移除）。
func TestMachineTunnelAgentsUpdate(t *testing.T) {
	reg, ws, _ := newMachineTunnelTestEnv(t)

	if err := ws.WriteJSON(tunnel.Frame{
		Type: tunnel.FrameAgents,
		Agents: []tunnel.AgentEntry{
			{AgentID: "ws-9", Name: "proj-9", RootPath: "/p/9"},
			{AgentID: "ws-10", Name: "proj-10", RootPath: "/p/10"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 5*time.Second, func() bool { return reg.AgentAlive("ws-10") })
	if _, ok := reg.FindAgent("blocked"); ok {
		t.Fatal("expected blocked to be dropped from advertised list")
	}
}

// TestMachineTunnelAuthRejectsBadToken 机器级隧道的鉴权：逐机器 token 不匹配必须拒绝，
// 且不得注册任何工作空间。
func TestMachineTunnelAuthRejectsBadToken(t *testing.T) {
	dir := t.TempDir()
	store := NewMachinesStore(filepath.Join(dir, "machines.json"))
	if _, err := store.IssueToken("m1"); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	ts := httptest.NewServer(Handler(reg, HandlerOptions{Machines: store}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/cata/v1/tunnel?machine=m1"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if err := ws.WriteJSON(tunnel.Frame{
		Type: tunnel.FrameHello, MachineTunnel: true, MachineID: "m1", MachineToken: "wrong",
		Protocol: tunnel.ProtocolName, Version: tunnel.Version,
		Agents: []tunnel.AgentEntry{{AgentID: "ws-9"}},
	}); err != nil {
		t.Fatal(err)
	}
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("expected error frame, got %v", err)
	}
	var f tunnel.Frame
	_ = json.Unmarshal(msg, &f)
	if f.Type != tunnel.FrameError || !strings.Contains(f.Message, "machine token") {
		t.Fatalf("expected machine token error, got %+v", f)
	}
	if reg.AgentAlive("ws-9") {
		t.Fatal("workspace must not be registered after failed auth")
	}
}

// TestMachineTunnelHelloGuards 机器级隧道的握手约束：必须带 machine_tunnel 标记、
// 且 hello 的 machine_id 必须与 ?machine= 一致（防张冠李戴）。
func TestMachineTunnelHelloGuards(t *testing.T) {
	reg := NewRegistry()
	ts := httptest.NewServer(Handler(reg, HandlerOptions{}))
	defer ts.Close()

	cases := []struct {
		name  string
		hello tunnel.Frame
		want  string
	}{
		{
			name:  "missing flag",
			hello: tunnel.Frame{Type: tunnel.FrameHello, MachineID: "m1", Protocol: tunnel.ProtocolName, Version: tunnel.Version},
			want:  "machine_tunnel",
		},
		{
			name:  "machine mismatch",
			hello: tunnel.Frame{Type: tunnel.FrameHello, MachineTunnel: true, MachineID: "m2", Protocol: tunnel.ProtocolName, Version: tunnel.Version},
			want:  "mismatch",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/cata/v1/tunnel?machine=m1"
			ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ws.Close()
			if err := ws.WriteJSON(c.hello); err != nil {
				t.Fatal(err)
			}
			ws.SetReadDeadline(time.Now().Add(3 * time.Second))
			_, msg, err := ws.ReadMessage()
			if err != nil {
				t.Fatalf("expected error frame, got %v", err)
			}
			var f tunnel.Frame
			_ = json.Unmarshal(msg, &f)
			if f.Type != tunnel.FrameError || !strings.Contains(f.Message, c.want) {
				t.Fatalf("want error containing %q, got %+v", c.want, f)
			}
		})
	}
}

// TestRegistryPrefersDirectAgentOverHub 同一工作空间既有自持隧道（legacy agent）又有
// 机器级隧道发布时，路由优先直连（真实在线进程），且列表里不重复、不标 Sleeping。
func TestRegistryPrefersDirectAgentOverHub(t *testing.T) {
	reg := NewRegistry()
	direct := &agentConn{info: AgentInfo{AgentID: "ws-1", Name: "direct", MachineID: "m1"}}
	hub := &agentConn{info: AgentInfo{AgentID: "machine:m1", MachineID: "m1"}}
	reg.RegisterHub(hub, "m1", []tunnel.AgentEntry{{AgentID: "ws-1", Name: "hub"}})
	reg.agents["ws-1"] = direct

	infos := reg.OnlineAgents()
	if len(infos) != 1 {
		t.Fatalf("OnlineAgents() = %+v, want a single entry", infos)
	}
	if infos[0].Sleeping || infos[0].Name != "direct" {
		t.Fatalf("direct connection should win: %+v", infos[0])
	}

	reg.mu.Lock()
	target, hint := reg.routeTargetLocked("ws-1")
	reg.mu.Unlock()
	if target != direct || hint != "" {
		t.Fatalf("route should be the direct conn with no agent hint, got target=%p hint=%q", target, hint)
	}

	// 仅有机器级隧道服务的工作空间：路由走隧道，并带目标 agent 提示。
	reg.mu.Lock()
	target, hint = reg.routeTargetLocked("only-on-hub")
	reg.mu.Unlock()
	if target != nil {
		t.Fatal("unknown workspace should not be routable")
	}
	reg.RegisterHub(hub, "m1", []tunnel.AgentEntry{{AgentID: "only-on-hub"}})
	reg.mu.Lock()
	target, hint = reg.routeTargetLocked("only-on-hub")
	reg.mu.Unlock()
	if target != hub || hint != "only-on-hub" {
		t.Fatalf("hub route should carry the agent hint, got target=%p hint=%q", target, hint)
	}
}

// TestDisconnectAgentRevokesHubServed 吊销机器级隧道服务的工作空间：立即停止路由，
// 并通知 supervisor（revoke 帧）。
func TestDisconnectAgentRevokesHubServed(t *testing.T) {
	reg, _, hub := newMachineTunnelTestEnv(t)

	reg.DisconnectAgent("ws-9")
	if reg.AgentAlive("ws-9") {
		t.Fatal("revoked workspace should not be routable")
	}
	// supervisor 侧应收到 revoke 帧（供其停止服务与拉起的 agent）。
	waitUntil(t, 5*time.Second, func() bool {
		for _, id := range hub.revokedIDs() {
			if id == "ws-9" {
				return true
			}
		}
		return false
	})
}
