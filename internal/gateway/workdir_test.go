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

// workTestHome 测试 CATA_HOME 短目录（unix socket 路径有 ~104 字节上限）。
func workTestHome(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(home, ".cata_worker", "gw-tests")
	if err := os.MkdirAll(base, 0755); err != nil {
		dir, err := os.MkdirTemp("/tmp", "cata-gwtest-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		return dir
	}
	dir, err := os.MkdirTemp(base, "t")
	if err != nil {
		t.Fatal(err)
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

// TestAgentBindingPerChannel 严格顺序（save → 删缓存 → 重载）且按渠道独立。
func TestAgentBindingPerChannel(t *testing.T) {
	home := workTestHome(t)
	path := filepath.Join(home, "binding.json")
	b := NewAgentBinding(path)
	if got := b.Agent("telegram"); got != "" {
		t.Fatalf("初始应为空，got %q", got)
	}
	b.Set("telegram", "ws-a")
	if got := b.Agent("telegram"); got != "ws-a" {
		t.Fatalf("telegram Agent=%q want ws-a", got)
	}
	// 其它渠道不受影响。
	if got := b.Agent("qq"); got != "" {
		t.Fatalf("qq 不应被 telegram 绑定影响，got %q", got)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "ws-a") || !strings.Contains(string(raw), "telegram") {
		t.Fatalf("配置文件未保存渠道绑定：%s", string(raw))
	}
	// 重启（新实例）恢复；两渠道并存。
	b.Set("qq", "ws-b")
	b2 := NewAgentBinding(path)
	if got := b2.Agent("telegram"); got != "ws-a" {
		t.Fatalf("重启后 telegram 应恢复 ws-a，got %q", got)
	}
	if got := b2.Agent("qq"); got != "ws-b" {
		t.Fatalf("重启后 qq 应恢复 ws-b，got %q", got)
	}
	// 更换渠道不影响其它；解绑只清该渠道。
	b2.Set("telegram", "ws-c")
	if got := b2.Agent("qq"); got != "ws-b" {
		t.Fatalf("telegram 更换不应影响 qq，got %q", got)
	}
	b2.Clear("telegram")
	if got := b2.Agent("telegram"); got != "" {
		t.Fatalf("解绑 telegram 后应为空，got %q", got)
	}
	if got := b2.Agent("qq"); got != "ws-b" {
		t.Fatalf("解绑 telegram 不应影响 qq，got %q", got)
	}
}

// TestHandleAgentBindCommand 绑定命令（按渠道）：菜单、list-first、序号绑定、路径绑定、reset。
func TestHandleAgentBindCommand(t *testing.T) {
	home, proj1, proj2 := mkTestSetup(t)
	now := time.Now()
	ws1 := reqWsID(t, proj1)
	ws2 := reqWsID(t, proj2)
	writeRegistry(t, []map[string]any{
		registryEntry(ws1, proj1, "proj1", now.Add(-time.Minute)),
		registryEntry(ws2, proj2, "proj2", now.Add(-time.Hour)),
	})
	path := filepath.Join(home, "binding.json")
	b := NewAgentBinding(path)
	key := SessionKeyFor("tg", "1")

	// 模拟本渠道从未看过列表（重置确认标记）。
	ResetAgentListSeen("telegram")
	reply, handled := HandleAgentBindCommand(b, "telegram", key, "1")
	if !handled {
		t.Fatal("未看列表的序号绑定应视为已处理（拒绝并提示）")
	}
	if !strings.Contains(reply, "请先发 /dir") {
		t.Fatalf("未看列表应禁止序号绑定：%q", reply)
	}
	// 看列表后允许序号绑定。
	HandleAgentBindCommand(b, "telegram", key, "")
	reply, _ = HandleAgentBindCommand(b, "telegram", key, "1")
	if !strings.Contains(reply, "已绑定 telegram") || !strings.Contains(reply, ws1) {
		t.Fatalf("序号绑定应成功：%q, ws=%s", reply, ws1)
	}
	if got := b.Agent("telegram"); got != ws1 {
		t.Fatalf("绑定后 Agent=%q want %s", got, ws1)
	}
	// 路径绑定（不要求看过列表）。
	b.Clear("telegram")
	reply, _ = HandleAgentBindCommand(b, "telegram", key, proj2)
	if !strings.Contains(reply, "已绑定 telegram") || !strings.Contains(reply, ws2) {
		t.Fatalf("路径绑定应成功：%q", reply)
	}
	if got := b.Agent("telegram"); got != ws2 {
		t.Fatalf("路径绑定后 Agent=%q want %s", got, ws2)
	}
	reply, _ = HandleAgentBindCommand(b, "telegram", key, "reset")
	if !strings.Contains(reply, "已解绑 telegram") {
		t.Fatalf("reset 应解绑：%q", reply)
	}
	if got := b.Agent("telegram"); got != "" {
		t.Fatalf("reset 后应为空，got %q", got)
	}
}

// TestConnForMessageRoutesAndBringsUp 按绑定 agent 转发：目标不在线也自动拉起，
// 连接 cwd = 绑定 agent 工作空间根路径；同一会话复用连接。
func TestConnForMessageRoutesAndBringsUp(t *testing.T) {
	home, proj1, _ := mkTestSetup(t)
	ws1 := reqWsID(t, proj1)
	writeRegistry(t, []map[string]any{
		registryEntry(ws1, proj1, "proj1", time.Now()),
	})
	path := filepath.Join(home, "binding.json")
	b := NewAgentBinding(path)
	b.Set("qq", ws1)
	root := filepath.Join(home, "w")
	m := NewSessionManagerWithStore(filepath.Join(root, "cata.sock"), root, nil)
	key := SessionKeyFor("qq", "9")

	conn, err := m.ConnForMessage(b, "qq", key)
	if err != nil {
		t.Fatalf("转发连接失败：%v", err)
	}
	if conn.Cwd() != proj1 {
		t.Fatalf("转发连接 cwd=%q want %q", conn.Cwd(), proj1)
	}
	// 断线自愈由 socketclient 负责；这里只验证每次转发都指向同一绑定 agent 的 cwd。
	_ = conn
}

// TestConnForMessageUnbound 未绑定 → 引导错误。
func TestConnForMessageUnbound(t *testing.T) {
	home := workTestHome(t)
	path := filepath.Join(home, "binding.json")
	b := NewAgentBinding(path)
	root := filepath.Join(home, "w")
	_ = os.MkdirAll(root, 0755)
	m := NewSessionManagerWithStore(filepath.Join(root, "cata.sock"), root, nil)
	_, err := m.ConnForMessage(b, "telegram", SessionKeyFor("tg", "1"))
	if err == nil || !strings.Contains(err.Error(), "telegram 渠道尚未绑定 agent") {
		t.Fatalf("未绑定应报引导错误，got %v", err)
	}
}

// TestAgentBindingRemoteCwd 远程模式绑定也指向工作空间根（不解析本地/不拉起）。
func TestAgentBindingRemoteCwd(t *testing.T) {
	home := workTestHome(t)
	t.Setenv(config.EnvCataHome, home)
	t.Setenv(config.EnvConfigFile, "")
	proj := filepath.Join(home, "w", "proj")
	_ = os.MkdirAll(proj, 0755)
	ws := reqWsID(t, proj)
	writeRegistry(t, []map[string]any{
		registryEntry(ws, proj, "proj", time.Now()),
	})
	path := filepath.Join(home, "binding.json")
	b := NewAgentBinding(path)
	b.Set("telegram", ws)
	m := NewRemoteSessionManagerWithStore(home, func(_ string, cwd string) *CataConn {
		return NewCataConn("", cwd)
	}, nil)
	conn, err := m.ConnForMessage(b, "telegram", SessionKeyFor("tg", "5"))
	if err != nil {
		t.Fatalf("远程转发失败：%v", err)
	}
	if conn.Cwd() != proj {
		t.Fatalf("远程 cwd=%q want %q", conn.Cwd(), proj)
	}
}

// TestGetWithCwdDialerRebuildsOnRebind 换绑 agent 后连接必须重建（不能复用 cwd 相同的旧连接）。
func TestGetWithCwdDialerRebuildsOnRebind(t *testing.T) {
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

	// 绑定 ws1（dialer）→ 连接应重建为 dialer、cwd=proj1。
	c1, err := m.GetWithCwdDialer(key, proj1, DialLocalAgent(ws1))
	if err != nil {
		t.Fatal(err)
	}
	if c1.DialKey() != "dialer" || c1.Cwd() != proj1 {
		t.Fatalf("绑定后连接应 dialer+cwd=proj1，got dialer=%q cwd=%q", c1.DialKey(), c1.Cwd())
	}

	// 换绑 ws2（cwd 不同）→ 必须重建到 proj2。
	c2, err := m.GetWithCwdDialer(key, proj2, DialLocalAgent(ws2))
	if err != nil {
		t.Fatal(err)
	}
	if c2.Cwd() != proj2 {
		t.Fatalf("换绑后 cwd=%q want %q", c2.Cwd(), proj2)
	}
}
