package tunnel

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"cata/internal/cata/tunnel"
)

// TestTunnelRoundtrip 端到端：worker（模拟 cata agent --link）注册到网关，
// 网关 DialAgent 打开 stream，字节经 WSS 帧在 stream 与本地 Unix socket 间往返。
func TestTunnelRoundtrip(t *testing.T) {
	// 模拟本地 agent：Unix socket 收到每行回显 "echo:<line>"。
	dir := t.TempDir()
	sockPath := dir + "/agent.sock"
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
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

	reg := NewRegistry()
	ts := httptest.NewServer(Handler(reg, HandlerOptions{Token: "tok"}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/cata/v1/tunnel?agent=ws-1"
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer tok")
	ws, resp, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		t.Fatalf("dial: %v (resp=%v)", err, resp)
	}
	defer ws.Close()

	if err := ws.WriteJSON(tunnel.Frame{
		Type: tunnel.FrameHello, AgentID: "ws-1", Name: "proj", RootPath: "/p",
		Protocol: tunnel.ProtocolName, Version: tunnel.Version,
	}); err != nil {
		t.Fatal(err)
	}

	waitUntil(t, 5*time.Second, func() bool { return reg.AgentAlive("ws-1") })

	// worker 读循环：处理 open/line/close。
	streams := map[uint64]net.Conn{}
	go func() {
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				return
			}
			var f tunnel.Frame
			if err := json.Unmarshal(msg, &f); err != nil {
				continue
			}
			switch f.Type {
			case tunnel.FrameOpen:
				c, err := net.Dial("unix", sockPath)
				if err != nil {
					_ = ws.WriteJSON(tunnel.Frame{Type: tunnel.FrameError, Stream: f.Stream, Message: err.Error()})
					continue
				}
				streams[f.Stream] = c
				_ = ws.WriteJSON(tunnel.Frame{Type: tunnel.FrameOpened, Stream: f.Stream})
				go func(id uint64, c net.Conn) {
					br := bufio.NewReader(c)
					for {
						line, err := br.ReadBytes('\n')
						if err != nil {
							_ = ws.WriteJSON(tunnel.Frame{Type: tunnel.FrameClose, Stream: id})
							_ = c.Close()
							return
						}
						if err := ws.WriteJSON(tunnel.Frame{Type: tunnel.FrameLine, Stream: id, Data: tunnel.EncodeData(line)}); err != nil {
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
			}
		}
	}()

	conn, err := reg.DialAgent("ws-1")
	if err != nil {
		t.Fatalf("dial agent: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("hi\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 64)
	got := readWithTimeout(t, conn, buf)
	if got != "echo:hi\n" {
		t.Fatalf("roundtrip got %q, want %q", got, "echo:hi\n")
	}

	// 关闭 worker 连接 → agent 应自动下线，stream 也应关闭。
	_ = ws.Close()
	waitUntil(t, 5*time.Second, func() bool { return !reg.AgentAlive("ws-1") })
}

func waitUntil(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func readWithTimeout(t *testing.T, conn net.Conn, buf []byte) string {
	t.Helper()
	ch := make(chan string, 1)
	go func() {
		n, err := conn.Read(buf)
		if err != nil {
			ch <- "<err:" + err.Error() + ">"
			return
		}
		ch <- string(buf[:n])
	}()
	select {
	case s := <-ch:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("read timeout")
		return ""
	}
}

// TestHandlerRequiresTokenAndHello 非 hello 首帧应拒绝（token 已移除：HTTP 握手不再校验
// gateway_token，鉴权靠 hello 帧 machine token；这里验证「首帧非 hello → FrameError」）。
func TestHandlerRequiresTokenAndHello(t *testing.T) {
	reg := NewRegistry()
	ts := httptest.NewServer(Handler(reg, HandlerOptions{}))
	defer ts.Close()

	// 无 token 也能完成 HTTP 握手（已不再要求 gateway_token），但首帧非 hello 应被拒绝。
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/cata/v1/tunnel?agent=ws-1"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial without token should succeed at HTTP layer now: %v", err)
	}
	defer ws.Close()
	if err := ws.WriteJSON(tunnel.Frame{Type: tunnel.FrameLine, Stream: 1, Data: tunnel.EncodeData([]byte("x"))}); err != nil {
		t.Fatal(err)
	}
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("expected error frame, got err: %v", err)
	}
	var f tunnel.Frame
	_ = json.Unmarshal(msg, &f)
	if f.Type != tunnel.FrameError {
		t.Fatalf("expected error frame, got %+v", f)
	}
	if reg.AgentAlive("ws-1") {
		t.Fatal("agent should not be registered after bad hello")
	}
}

// TestReRegisterReplacesStaleConnection 回归：同一 agent 第二次 hello 顶替旧连接，
// 而不是被 "already connected" 拒绝——避免 worker 重连被拒形成死锁。
func TestReRegisterReplacesStaleConnection(t *testing.T) {
	reg := NewRegistry()
	ts := httptest.NewServer(Handler(reg, HandlerOptions{Token: "tok"}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/cata/v1/tunnel?agent=ws-1"
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer tok")

	hello := tunnel.Frame{Type: tunnel.FrameHello, AgentID: "ws-1", Name: "proj",
		RootPath: "/p", Protocol: tunnel.ProtocolName, Version: tunnel.Version}

	// 第一次连接注册。
	ws1, _, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		t.Fatal(err)
	}
	if err := ws1.WriteJSON(hello); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 5*time.Second, func() bool { return reg.AgentAlive("ws-1") })

	// 第二次连接同 agent：应顶替旧连接（旧 ws1 被网关关闭），注册仍成功。
	ws2, _, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		t.Fatal(err)
	}
	defer ws2.Close()
	if err := ws2.WriteJSON(hello); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 5*time.Second, func() bool {
		info, ok := reg.FindAgent("ws-1")
		return ok && info.AgentID == "ws-1"
	})

	// 旧连接应被网关关闭（readLoop 返回 EOF）。
	ws1.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := ws1.ReadMessage(); err == nil {
		t.Fatal("expected stale ws1 to be closed by gateway")
	}
}

// TestRegistryMachineGrouping 验证机器标识分组：Machines 去重、FindAgentByMachine 按机器找 agent。
func TestRegistryMachineGrouping(t *testing.T) {
	reg := NewRegistry()
	// 直接注入 agentConn（同包测试可访问内部字段）。
	reg.agents["ws-a"] = &agentConn{info: AgentInfo{AgentID: "ws-a", MachineID: "m1"}}
	reg.agents["ws-b"] = &agentConn{info: AgentInfo{AgentID: "ws-b", MachineID: "m1"}}
	reg.agents["ws-c"] = &agentConn{info: AgentInfo{AgentID: "ws-c", MachineID: "m2"}}

	machines := reg.Machines()
	if len(machines) != 2 {
		t.Fatalf("Machines() = %v, want 2 unique machines", machines)
	}

	a := reg.FindAgentByMachine("m1")
	if a == nil || (a.info.AgentID != "ws-a" && a.info.AgentID != "ws-b") {
		t.Fatalf("FindAgentByMachine(m1) should return an m1 agent, got %+v", a)
	}
	if reg.FindAgentByMachine("m2").info.AgentID != "ws-c" {
		t.Fatalf("FindAgentByMachine(m2) should return ws-c")
	}
	if reg.FindAgentByMachine("m3") != nil {
		t.Fatal("FindAgentByMachine(m3) should be nil")
	}
}
