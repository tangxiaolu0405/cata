package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHandleWorkdirCommand 覆盖 /dir 命令：显示当前、~ 展开、不存在、切换、重复切换。
func TestHandleWorkdirCommand(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0755); err != nil {
		t.Fatal(err)
	}
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

	// 存在目录 → 切换成功，会话连接 cwd 更新。
	reply, _ = HandleWorkdirCommand(m, key, proj)
	if !strings.Contains(reply, "产出区已切换") {
		t.Fatalf("应提示切换成功：%q", reply)
	}
	if cur := m.CurrentCwd(key); cur != proj {
		t.Fatalf("切换后 cwd=%q want %q", cur, proj)
	}

	// 相同路径 → 已在产出区。
	reply, _ = HandleWorkdirCommand(m, key, proj)
	if !strings.Contains(reply, "已在产出区") {
		t.Fatalf("重复切换应提示已在：%q", reply)
	}

	// ~ 展开：切到存在的 home 子目录。
	home := t.TempDir()
	t.Setenv("HOME", home)
	sub := filepath.Join(home, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	reply, _ = HandleWorkdirCommand(m, key, "~/sub")
	if !strings.Contains(reply, "产出区已切换") {
		t.Fatalf("~/ 路径应展开并切换：%q", reply)
	}
	if cur := m.CurrentCwd(key); cur != sub {
		t.Fatalf("~/sub 应展开到 %q，got %q", sub, cur)
	}
}
