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

// stubAgentSocket 预置目标目录工作区的哑 per-ws socket：
// link.EnsureAgent 第一步 ping 成功即视为「在线」，避免测试真实拉起 agent 进程。
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

// TestHandleWorkdirCommand 覆盖 /dir 命令：显示当前、~ 展开、不存在、切换、重复切换。
// 切换目标均预置「在线」agent（哑 socket），验证自动就绪提示与 cwd 生效。
func TestHandleWorkdirCommand(t *testing.T) {
	// CATA_HOME 用短 /tmp 路径：unix socket 路径有 ~104 字节上限（temp 目录太长会 bind 失败）。
	home, err := os.MkdirTemp("/tmp", "cata-wtest-")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvCataHome, home)
	t.Setenv(config.EnvConfigFile, "")
	// worker 根与目标目录也放短路径下：ws id 由路径派生，过长会导致 socket 路径超限。
	root := filepath.Join(home, "w")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0755); err != nil {
		t.Fatal(err)
	}
	stubAgentSocket(t, proj)
	m := NewSessionManager(filepath.Join(root, "cata.sock"), root)
	key := SessionKeyFor("test", "1")

	// 无参数 → 显示当前产出区（此时未切换，即 worker 目录）。
	reply, handled := HandleWorkdirCommand(m, key, "")
	if !handled {
		t.Fatal("空参数应视为已处理")
	}
	if !strings.Contains(reply, "当前产出区") {
		t.Fatalf("空参数应显示当前目录：%q", reply)
	}

	// 不存在目录 → 报错且不切换。
	reply, _ = HandleWorkdirCommand(m, key, filepath.Join(root, "nope"))
	if !strings.Contains(reply, "目录不存在") {
		t.Fatalf("应提示目录不存在：%q", reply)
	}
	if cur := m.CurrentCwd(key); cur == proj {
		t.Fatal("失败的切换不应改变 cwd")
	}

	// 存在目录（agent 已就绪）→ 切换成功，会话连接 cwd 更新，并提示自动就绪。
	reply, _ = HandleWorkdirCommand(m, key, proj)
	if !strings.Contains(reply, "产出区已切换") {
		t.Fatalf("应提示切换成功：%q", reply)
	}
	if !strings.Contains(reply, "已自动就绪") {
		t.Fatalf("agent 就绪时应提示：%q", reply)
	}
	if cur := m.CurrentCwd(key); cur != proj {
		t.Fatalf("切换后 cwd=%q want %q", cur, proj)
	}

	// 相同路径 → 已在产出区。
	reply, _ = HandleWorkdirCommand(m, key, proj)
	if !strings.Contains(reply, "已在产出区") {
		t.Fatalf("重复切换应提示已在：%q", reply)
	}

	// ~ 展开：切到存在的 home 子目录（HOME 也放短路径下）。
	hh := filepath.Join(home, "h")
	if err := os.MkdirAll(hh, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", hh)
	sub := filepath.Join(hh, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	stubAgentSocket(t, sub)
	reply, _ = HandleWorkdirCommand(m, key, "~/sub")
	if !strings.Contains(reply, "产出区已切换") {
		t.Fatalf("~/ 路径应展开并切换：%q", reply)
	}
	if cur := m.CurrentCwd(key); cur != sub {
		t.Fatalf("~/sub 应展开到 %q，got %q", sub, cur)
	}
}

// TestHandleWorkdirRemote 远程模式：切换走默认云端连接（不解析本地工作区/不拉起），cwd 仍生效。
func TestHandleWorkdirRemote(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0755); err != nil {
		t.Fatal(err)
	}
	m := NewRemoteSessionManager(root, func(_ string, cwd string) *CataConn {
		return NewCataConn("", cwd)
	})
	if !m.IsRemote() {
		t.Fatal("远程 manager 应标记 remote")
	}
	key := SessionKeyFor("tg", "9")
	reply, _ := HandleWorkdirCommand(m, key, proj)
	if !strings.Contains(reply, "产出区已切换") {
		t.Fatalf("远程模式应可切换 cwd：%q", reply)
	}
	if cur := m.CurrentCwd(key); cur != proj {
		t.Fatalf("远程模式 cwd=%q want %q", cur, proj)
	}
}

// writeRegistry 预置注册表（短 CATA_HOME 下）：让 /dir 候选列表可测。
func writeRegistry(t *testing.T, entries []map[string]any) {
	t.Helper()
	dir := filepath.Join(os.Getenv(config.EnvCataHome), "registry")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"workspaces": entries})
	if err := os.WriteFile(filepath.Join(dir, "workspaces.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

// TestHandleWorkdirByIndex 无需记住路径：/dir 列候选，/dir <序号> 切换。
func TestHandleWorkdirByIndex(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "cata-wtest-")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvCataHome, home)
	t.Setenv(config.EnvConfigFile, "")
	root := filepath.Join(home, "w")
	_ = os.MkdirAll(root, 0755)
	old := filepath.Join(root, "old")
	stock := filepath.Join(root, "stock")
	_ = os.MkdirAll(old, 0755)
	_ = os.MkdirAll(stock, 0755)
	stubAgentSocket(t, old)
	stubAgentSocket(t, stock)
	// 注册表：stock 最近使用（排第一），old 较旧。
	now := time.Now()
	wsStock, err := brain.ResolveWorkspaceNoGlobal(stock)
	if err != nil {
		t.Fatal(err)
	}
	wsOld, err := brain.ResolveWorkspaceNoGlobal(old)
	if err != nil {
		t.Fatal(err)
	}
	writeRegistry(t, []map[string]any{
		{"id": wsStock.ID, "root_path": stock, "kind": "git", "name": "stock",
			"created_at":   now.Add(-2 * time.Hour).Format(time.RFC3339),
			"last_seen_at": now.Add(-time.Minute).Format(time.RFC3339)},
		{"id": wsOld.ID, "root_path": old, "kind": "git", "name": "old",
			"created_at":   now.Add(-24 * time.Hour).Format(time.RFC3339),
			"last_seen_at": now.Add(-10 * time.Hour).Format(time.RFC3339)},
	})

	m := NewSessionManager(filepath.Join(root, "cata.sock"), root)
	key := SessionKeyFor("test", "7")

	// /dir 无参 → 菜单列出候选（stock 在前）。
	reply, _ := HandleWorkdirCommand(m, key, "")
	if !strings.Contains(reply, "1. stock") || !strings.Contains(reply, "/dir <序号>") {
		t.Fatalf("/dir 菜单应列出带序号的候选：\n%s", reply)
	}
	if !strings.Contains(reply, "2. old") {
		t.Fatalf("菜单应含第二个候选：\n%s", reply)
	}

	// /dir 1 → 切到列表第一项（stock）。
	reply, _ = HandleWorkdirCommand(m, key, "1")
	if !strings.Contains(reply, stock) || !strings.Contains(reply, "产出区已切换") {
		t.Fatalf("/dir 1 应切到 stock：\n%s", reply)
	}
	if cur := m.CurrentCwd(key); cur != stock {
		t.Fatalf("cwd=%q want %q", cur, stock)
	}
	// /dir 1 再次 → 已在。
	reply, _ = HandleWorkdirCommand(m, key, "1")
	if !strings.Contains(reply, "已在产出区") {
		t.Fatalf("重复序号切换应提示已在：\n%s", reply)
	}
}

// TestHandleWorkdirIndexOutOfRange 序号越界 → 明确提示。
func TestHandleWorkdirIndexOutOfRange(t *testing.T) {
	home, _ := os.MkdirTemp("/tmp", "cata-wtest-")
	t.Setenv(config.EnvCataHome, home)
	t.Setenv(config.EnvConfigFile, "")
	root := filepath.Join(home, "w")
	_ = os.MkdirAll(root, 0755)
	proj := filepath.Join(root, "proj")
	_ = os.MkdirAll(proj, 0755)
	now := time.Now()
	writeRegistry(t, []map[string]any{
		{"id": "w-proj", "root_path": proj, "kind": "git", "name": "proj",
			"created_at": now.Format(time.RFC3339), "last_seen_at": now.Format(time.RFC3339)},
	})
	m := NewSessionManager(filepath.Join(root, "cata.sock"), root)
	key := SessionKeyFor("test", "8")
	reply, _ := HandleWorkdirCommand(m, key, "99")
	if !strings.Contains(reply, "序号无效") {
		t.Fatalf("越界序号应提示：%q", reply)
	}
	if cur := m.CurrentCwd(key); cur == proj {
		t.Fatal("越界序号不应切换")
	}
}
