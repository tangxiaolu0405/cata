package gateway

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
	"cata/internal/cata/socketclient"
)

// workTestHome 测试 CATA_HOME：返回一个**足够短**的目录。
// unix socket 路径上限约 104 字节，而 per-ws socket 路径 = {home}/sockets/<ws_id>.sock，
// ws_id 又由 home 路径派生——home 太长会直接 bind 失败（CI 上 ~/.cata_worker 可写即踩中）。
// 统一用 /tmp 下的短目录（Linux/macOS 均存在且短），保证各平台一致稳定。
func workTestHome(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "cw")
	if err != nil {
		// 受限环境（无 /tmp 写权限）：退化为系统临时目录。
		dir, err = os.MkdirTemp("", "cw")
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// stubAgentSocket 预置目标目录工作区的哑 per-ws socket（pong 响应），
// 让 link.EnsureAgent 第一步 ping 成功，避免测试真实拉起 agent 进程。
func stubAgentSocket(t *testing.T, cwd string) {
	t.Helper()
	ws, err := brain.ResolveWorkspaceNoGlobal(cwd)
	if err != nil {
		t.Fatal(err)
	}
	sock := config.ResolvedAgentSocketPath(ws.ID)
	if err := os.MkdirAll(filepath.Dir(sock), 0755); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
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
			go func(cc net.Conn) {
				defer cc.Close()
				br := bufio.NewReader(cc)
				for {
					if _, err := br.ReadBytes('\n'); err != nil {
						return
					}
					_, _ = cc.Write([]byte(`{"success":true,"message":"pong"}` + "\n"))
				}
			}(c)
		}
	}()
	if err := socketclient.Ping(sock); err != nil {
		t.Fatalf("stub socket %s not pingable: %v", sock, err)
	}
}

// mkTestSetup 建两个工作区目录并预置在线 agent socket。
func mkTestSetup(t *testing.T) (home, proj1, proj2 string) {
	t.Helper()
	home = workTestHome(t)
	t.Setenv(config.EnvCataHome, home)
	t.Setenv(config.EnvConfigFile, "")
	root := filepath.Join(home, "w")
	_ = os.MkdirAll(root, 0755)
	proj1 = filepath.Join(root, "proj1")
	proj2 = filepath.Join(root, "proj2")
	_ = os.MkdirAll(proj1, 0755)
	_ = os.MkdirAll(proj2, 0755)
	stubAgentSocket(t, proj1)
	stubAgentSocket(t, proj2)
	return home, proj1, proj2
}

// writeRegistry 预置注册表。
func writeRegistry(t *testing.T, entries []map[string]any) {
	t.Helper()
	home := os.Getenv(config.EnvCataHome)
	dir := filepath.Join(home, "registry")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"workspaces": entries})
	if err := os.WriteFile(filepath.Join(dir, "workspaces.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

func registryEntry(id, root, name string, seen time.Time) map[string]any {
	return map[string]any{
		"id": id, "root_path": root, "kind": "git", "name": name,
		"created_at": seen.Format(time.RFC3339), "last_seen_at": seen.Format(time.RFC3339),
	}
}

// reqWsID 解析目录的工作区 id。
func reqWsID(t *testing.T, cwd string) string {
	t.Helper()
	ws, err := brain.ResolveWorkspaceNoGlobal(cwd)
	if err != nil {
		t.Fatal(err)
	}
	return ws.ID
}

// TestReplyForWorkdir /dir 切换工作空间：列表、list-first、序号切换、路径切换、reset。
func TestReplyForWorkdir(t *testing.T) {
	home, proj1, proj2 := mkTestSetup(t)
	now := time.Now()
	ws1 := reqWsID(t, proj1)
	ws2 := reqWsID(t, proj2)
	writeRegistry(t, []map[string]any{
		registryEntry(ws1, proj1, "proj1", now.Add(-time.Minute)),
		registryEntry(ws2, proj2, "proj2", now.Add(-time.Hour)),
	})
	root := filepath.Join(home, "w")
	m := NewSessionManagerWithStore(filepath.Join(root, "cata.sock"), root, NewSessionCwdStore(filepath.Join(home, "cwd.json")))
	key := SessionKeyFor("tg", "1")

	// 未看列表的序号切换应拒绝并提示。
	reply := ReplyForWorkdir(m, "telegram", key, "1")
	if !strings.Contains(reply, "请先发 /dir") {
		t.Fatalf("未看列表应禁止序号切换：%q", reply)
	}
	// 看列表后允许序号切换。
	ReplyForWorkdir(m, "telegram", key, "")
	reply = ReplyForWorkdir(m, "telegram", key, "1")
	if !strings.Contains(reply, "已切换本会话工作区") || !strings.Contains(reply, proj1) {
		t.Fatalf("序号切换应成功：%q", reply)
	}
	if got := m.CwdOverride(key); got != proj1 {
		t.Fatalf("切换后 CwdOverride=%q want %q", got, proj1)
	}
	// 路径切换（不要求看过列表）。
	reply = ReplyForWorkdir(m, "telegram", key, proj2)
	if !strings.Contains(reply, "已切换本会话工作区") || !strings.Contains(reply, proj2) {
		t.Fatalf("路径切换应成功：%q", reply)
	}
	if got := m.CwdOverride(key); got != proj2 {
		t.Fatalf("路径切换后 CwdOverride=%q want %q", got, proj2)
	}
	// reset 恢复默认。
	reply = ReplyForWorkdir(m, "telegram", key, "reset")
	if !strings.Contains(reply, "已恢复默认产出区") {
		t.Fatalf("reset 应恢复默认：%q", reply)
	}
	if got := m.CwdOverride(key); got != "" {
		t.Fatalf("reset 后 CwdOverride=%q want empty", got)
	}
}

// TestConnForMessageRoutesToDir /dir 切换后转发到该工作空间 agent（本地拉起 + cwd=工作区根）。
func TestConnForMessageRoutesToDir(t *testing.T) {
	home, proj1, _ := mkTestSetup(t)
	ws1 := reqWsID(t, proj1)
	writeRegistry(t, []map[string]any{
		registryEntry(ws1, proj1, "proj1", time.Now()),
	})
	root := filepath.Join(home, "w")
	m := NewSessionManagerWithStore(filepath.Join(root, "cata.sock"), root, NewSessionCwdStore(filepath.Join(home, "cwd.json")))
	key := SessionKeyFor("qq", "9")
	// /dir 切到 proj1。
	ReplyForWorkdir(m, "qq", key, proj1)

	conn, err := m.ConnForMessage(Config{}, "qq", key)
	if err != nil {
		t.Fatalf("转发连接失败：%v", err)
	}
	if conn.Cwd() != proj1 {
		t.Fatalf("转发连接 cwd=%q want %q", conn.Cwd(), proj1)
	}
	if conn.DialKey() != "dialer" {
		t.Fatalf("转发连接应带 dialer（拨工作空间 agent），got %q", conn.DialKey())
	}
}

// TestConnForMessageNoDir 无 /dir 切换时用默认转发目标（第一个注册工作区）。
func TestConnForMessageNoDir(t *testing.T) {
	home, proj1, _ := mkTestSetup(t)
	ws1 := reqWsID(t, proj1)
	writeRegistry(t, []map[string]any{
		registryEntry(ws1, proj1, "proj1", time.Now()),
	})
	root := filepath.Join(home, "w")
	m := NewSessionManagerWithStore(filepath.Join(root, "cata.sock"), root, nil)
	key := SessionKeyFor("qq", "9")

	conn, err := m.ConnForMessage(Config{}, "qq", key)
	if err != nil {
		t.Fatalf("转发连接失败：%v", err)
	}
	if conn.Cwd() != proj1 {
		t.Fatalf("默认转发 cwd=%q want %q", conn.Cwd(), proj1)
	}
	if conn.DialKey() != "dialer" {
		t.Fatalf("默认转发应带 dialer，got %q", conn.DialKey())
	}
}

// TestConnForMessageNoTarget 无注册工作区且无 /dir → 引导错误。
func TestConnForMessageNoTarget(t *testing.T) {
	home := workTestHome(t)
	t.Setenv(config.EnvCataHome, home)
	t.Setenv(config.EnvConfigFile, "")
	root := filepath.Join(home, "w")
	_ = os.MkdirAll(root, 0755)
	m := NewSessionManagerWithStore(filepath.Join(root, "cata.sock"), root, nil)
	_, err := m.ConnForMessage(Config{}, "telegram", SessionKeyFor("tg", "1"))
	if err == nil || !strings.Contains(err.Error(), "无可用转发目标") {
		t.Fatalf("应报无目标错误，got %v", err)
	}
}

// TestConnForMessageRemoteRoutesToDir 远程模式 /dir 切换后拨该工作空间 agent 的隧道。
func TestConnForMessageRemoteRoutesToDir(t *testing.T) {
	home := workTestHome(t)
	t.Setenv(config.EnvCataHome, home)
	t.Setenv(config.EnvConfigFile, "")
	proj := filepath.Join(home, "w", "proj")
	_ = os.MkdirAll(proj, 0755)
	ws := reqWsID(t, proj)
	writeRegistry(t, []map[string]any{
		registryEntry(ws, proj, "proj", time.Now()),
	})

	var dialed []string
	m := NewRemoteSessionManagerWithStore(home, func(_ string, cwd string) *CataConn {
		return NewCataConn("", cwd)
	}, NewSessionCwdStore(filepath.Join(home, "cwd.json")))
	m.remoteDial = func(agentID string) func() (net.Conn, error) {
		dialed = append(dialed, agentID)
		return func() (net.Conn, error) { return &eofConn{}, nil }
	}
	key := SessionKeyFor("tg", "5")
	// /dir 切到 proj。
	ReplyForWorkdir(m, "telegram", key, proj)

	conn, err := m.ConnForMessage(Config{}, "telegram", key)
	if err != nil {
		t.Fatalf("远程转发失败：%v", err)
	}
	if len(dialed) != 1 || dialed[0] != ws {
		t.Fatalf("应拨 /dir 工作空间 agent 的隧道，dialed=%v want [%s]", dialed, ws)
	}
	if conn.Cwd() != proj {
		t.Fatalf("远程 cwd=%q want %q", conn.Cwd(), proj)
	}
	if conn.DialKey() != "dialer" {
		t.Fatalf("远程转发连接应带隧道 dialer，got %q", conn.DialKey())
	}
}

// TestGetWithCwdDialerRebuildsOnSwitch /dir 切换后连接必须重建（不能复用 cwd 相同的旧连接）。
func TestGetWithCwdDialerRebuildsOnSwitch(t *testing.T) {
	home, proj1, proj2 := mkTestSetup(t)
	ws1 := reqWsID(t, proj1)
	ws2 := reqWsID(t, proj2)
	root := filepath.Join(home, "w")
	m := NewSessionManagerWithStore(filepath.Join(root, "cata.sock"), root, nil)
	key := SessionKeyFor("qq", "1")

	// 先建默认（worker，无 dialer）连接。
	c0, err := m.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if c0.DialKey() != "" {
		t.Fatalf("默认连接应无 dialer，got %q", c0.DialKey())
	}

	// /dir 切到 ws1（dialer）→ 连接应重建为 dialer、cwd=proj1。
	c1, err := m.GetWithCwdDialer(key, proj1, DialLocalAgent(ws1))
	if err != nil {
		t.Fatal(err)
	}
	if c1.DialKey() != "dialer" || c1.Cwd() != proj1 {
		t.Fatalf("切换后连接应 dialer+cwd=proj1，got dialer=%q cwd=%q", c1.DialKey(), c1.Cwd())
	}

	// 切到 ws2（cwd 不同）→ 必须重建到 proj2。
	c2, err := m.GetWithCwdDialer(key, proj2, DialLocalAgent(ws2))
	if err != nil {
		t.Fatal(err)
	}
	if c2.Cwd() != proj2 {
		t.Fatalf("再切换后 cwd=%q want %q", c2.Cwd(), proj2)
	}
}
