package gateway

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
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
