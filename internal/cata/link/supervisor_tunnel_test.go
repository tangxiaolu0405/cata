package link

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
	"cata/internal/cata/tunnel"
)

// hubTestEnv 准备一台「本机」：短路径 CATA_HOME（Unix socket 有长度上限）、
// link.json 里注册一个工作空间、以及该工作空间的 per-ws socket（模拟已运行的 agent）。
type hubTestEnv struct {
	cfg      Config
	sockPath string
}

func newHubTestEnv(t *testing.T) *hubTestEnv {
	t.Helper()
	home, err := os.MkdirTemp("", "ch")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("CATA_HOME", home)
	if err := brain.EnsureCataLayout(); err != nil {
		t.Fatal(err)
	}

	sockPath := config.ResolvedAgentSocketPath("ws-a")
	if err := os.MkdirAll(filepath.Dir(sockPath), 0755); err != nil {
		t.Fatal(err)
	}
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
				// 最小 agent 协议：ping 回 pong（EnsureAgent 的存活探测），其余行回显。
				br := bufio.NewReader(c)
				for {
					line, err := br.ReadBytes('\n')
					if err != nil {
						return
					}
					if strings.Contains(string(line), `"command":"ping"`) {
						_, _ = c.Write([]byte("{\"success\":true,\"message\":\"pong\"}\n"))
						continue
					}
					_, _ = c.Write(append([]byte("echo:"), line...))
				}
			}(c)
		}
	}()

	cfg := Config{
		GatewayURL:   "http://127.0.0.1:0",
		MachineID:    "m1",
		MachineToken: "m-tok",
		Agents: map[string]AgentEntry{
			"ws-a": {AgentID: "ws-a", Name: "proj-a", RootPath: "/p/a", Enabled: true, KeepAlive: true},
			// 未启用的工作空间不应出现在对外清单里。
			"ws-off": {AgentID: "ws-off", Name: "off", RootPath: "/p/off", Enabled: false},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	return &hubTestEnv{cfg: cfg, sockPath: sockPath}
}

// TestMachineTunnelWorkerSpawnsAndForwards supervisor 侧（worker）：机器级隧道 hello 带
// machine_tunnel 标记与工作空间清单；gateway 下发 open{AgentID} 时拨该工作空间的
// per-ws socket 并双向转发；未知/已吊销工作空间回错误帧。
func TestMachineTunnelWorkerSpawnsAndForwards(t *testing.T) {
	env := newHubTestEnv(t)

	type result struct {
		hello     tunnel.Frame
		line      string
		ghostErr  string
		revokedEr string
	}
	resCh := make(chan result, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("machine") != "m1" {
			t.Errorf("machine query = %q, want m1", r.URL.RawQuery)
		}
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		ws.SetReadLimit(int64(tunnel.MaxFrameBytes))

		var res result
		var hello tunnel.Frame
		_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		if err := ws.ReadJSON(&hello); err != nil {
			resCh <- res
			return
		}
		res.hello = hello

		send := func(f tunnel.Frame) { _ = ws.WriteJSON(f) }
		// 1) 已注册工作空间：open → opened → 字节往返。
		send(tunnel.Frame{Type: tunnel.FrameOpen, Stream: 1, AgentID: "ws-a"})
		res.line = waitLine(t, ws, 1)
		// 2) 未注册工作空间：open → 错误帧（不得静默挂起）。
		send(tunnel.Frame{Type: tunnel.FrameOpen, Stream: 2, AgentID: "ghost"})
		res.ghostErr = waitError(t, ws, 2)
		// 3) 网关吊销后：open → 错误帧。
		send(tunnel.Frame{Type: tunnel.FrameRevoke, AgentID: "ws-a"})
		time.Sleep(200 * time.Millisecond)
		send(tunnel.Frame{Type: tunnel.FrameOpen, Stream: 3, AgentID: "ws-a"})
		res.revokedEr = waitError(t, ws, 3)
		resCh <- res
	}))
	defer srv.Close()

	cfg := env.cfg
	cfg.GatewayURL = srv.URL
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := &machineHub{backoff: newEnsureBackoff(), denied: map[string]bool{}}
	go func() { _ = hub.runOnce(ctx, cfg) }()

	select {
	case res := <-resCh:
		if !res.hello.MachineTunnel || res.hello.MachineID != "m1" {
			t.Fatalf("hello not a machine tunnel: %+v", res.hello)
		}
		ids := make([]string, 0, len(res.hello.Agents))
		for _, a := range res.hello.Agents {
			ids = append(ids, a.AgentID)
		}
		if len(ids) != 1 || ids[0] != "ws-a" {
			t.Fatalf("advertised workspaces = %v, want [ws-a]", ids)
		}
		if res.line != "echo:hi\n" {
			t.Fatalf("stream roundtrip = %q, want %q", res.line, "echo:hi\n")
		}
		if !strings.Contains(res.ghostErr, "not registered") {
			t.Fatalf("unknown workspace error = %q, want 'not registered'", res.ghostErr)
		}
		if !strings.Contains(res.revokedEr, "revoked") {
			t.Fatalf("revoked workspace error = %q, want 'revoked'", res.revokedEr)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("gateway-side assertions did not complete in time")
	}
}

// waitLine 打开 stream 后写一行，并返回 worker 回传的 line 帧内容。
func waitLine(t *testing.T, ws *websocket.Conn, stream uint64) string {
	t.Helper()
	for {
		_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, msg, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		var f tunnel.Frame
		if err := json.Unmarshal(msg, &f); err != nil {
			continue
		}
		switch f.Type {
		case tunnel.FrameOpened:
			if err := ws.WriteJSON(tunnel.Frame{Type: tunnel.FrameLine, Stream: stream, Data: tunnel.EncodeData([]byte("hi\n"))}); err != nil {
				t.Fatalf("write line: %v", err)
			}
		case tunnel.FrameLine:
			data, err := tunnel.DecodeData(f.Data)
			if err != nil {
				continue
			}
			return string(data)
		case tunnel.FrameError:
			t.Fatalf("unexpected error frame: %s", f.Message)
		case tunnel.FramePing:
			_ = ws.WriteJSON(tunnel.Frame{Type: tunnel.FramePong})
		}
	}
}

// waitError 等待指定 stream 的错误帧并返回其 message。
func waitError(t *testing.T, ws *websocket.Conn, stream uint64) string {
	t.Helper()
	for {
		_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, msg, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		var f tunnel.Frame
		if err := json.Unmarshal(msg, &f); err != nil {
			continue
		}
		if f.Type == tunnel.FrameError && f.Stream == stream {
			return f.Message
		}
		if f.Type == tunnel.FramePing {
			_ = ws.WriteJSON(tunnel.Frame{Type: tunnel.FramePong})
		}
	}
}

// TestMachineTunnelWSURL 机器级隧道端点：?machine=<id>，scheme 归一化。
func TestMachineTunnelWSURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://gw.example.com", "wss://gw.example.com/cata/v1/tunnel?machine=m1"},
		{"http://127.0.0.1:8787", "ws://127.0.0.1:8787/cata/v1/tunnel?machine=m1"},
		{"wss://gw.example.com/base", "wss://gw.example.com/base/cata/v1/tunnel?machine=m1"},
	}
	for _, c := range cases {
		got, err := machineTunnelWSURL(c.in, "m1")
		if err != nil {
			t.Fatalf("machineTunnelWSURL(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("machineTunnelWSURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if _, err := machineTunnelWSURL("ftp://x", "m1"); err == nil {
		t.Fatal("unsupported scheme should error")
	}
}

// TestMachineHubEntriesAndRevoke 对外清单只含启用的工作空间；吊销后立即剔除。
func TestMachineHubEntriesAndRevoke(t *testing.T) {
	env := newHubTestEnv(t)
	hub := &machineHub{backoff: newEnsureBackoff(), denied: map[string]bool{}}

	entries := hub.listEntries(env.cfg)
	if len(entries) != 1 || entries[0].AgentID != "ws-a" {
		t.Fatalf("listEntries = %+v, want only ws-a", entries)
	}
	if entries[0].Name != "proj-a" || entries[0].RootPath != "/p/a" {
		t.Fatalf("entry info mismatch: %+v", entries[0])
	}
	hub.setEntries(entries)
	hub.revoke("ws-a")
	if got := hub.listEntries(env.cfg); len(got) != 0 {
		t.Fatalf("revoked workspace still advertised: %+v", got)
	}
	if !hub.isDenied("ws-a") {
		t.Fatal("revoked workspace should be denied")
	}
}

// TestMachineHubEnsureAndDialRejectsUnknown 未在本机注册的工作空间不允许经隧道转发
// （机器级隧道只服务 link.json 里的工作空间，防止越权唤醒任意工作空间）。
func TestMachineHubEnsureAndDialRejectsUnknown(t *testing.T) {
	newHubTestEnv(t)
	hub := &machineHub{backoff: newEnsureBackoff(), denied: map[string]bool{}}
	if _, err := hub.ensureAndDial("ghost"); err == nil {
		t.Fatal("unknown workspace should be rejected")
	}
	if _, err := hub.ensureAndDial(""); err == nil {
		t.Fatal("empty agent id should be rejected")
	}
}
