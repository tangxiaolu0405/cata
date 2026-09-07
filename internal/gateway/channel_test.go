package gateway

import (
	"context"
	"testing"
	"time"
)

// TestPendingManagerExec 验证 exec 确认的注册/resolve/超时清理。
func TestPendingManagerExec(t *testing.T) {
	p := NewPendingManager()
	ch, cleanup := p.RegisterExec("c1")
	defer cleanup()

	// resolve 前 HasPendingExec 应命中。
	if id, _, ok := p.HasPendingExec(); !ok || id != "c1" {
		t.Fatalf("HasPendingExec got id=%q ok=%v", id, ok)
	}

	// resolve 后通道收到值，且 pending 清空。
	p.ResolveExec("c1", true)
	select {
	case ok := <-ch:
		if !ok {
			t.Fatal("expected approved=true")
		}
	case <-time.After(time.Second):
		t.Fatal("exec confirm not resolved")
	}
	if _, _, ok := p.HasPendingExec(); ok {
		t.Fatal("pending exec should be cleared after resolve")
	}
}

// TestPendingManagerChoice 验证选择的注册/resolve。
func TestPendingManagerChoice(t *testing.T) {
	p := NewPendingManager()
	opts := []ChoiceOption{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}
	ch, cleanup := p.RegisterChoice("ch1", opts)
	defer cleanup()

	if id, _, got, ok := p.HasPendingChoice(); !ok || id != "ch1" || len(got) != 2 {
		t.Fatalf("HasPendingChoice id=%q opts=%d ok=%v", id, len(got), ok)
	}

	p.ResolveChoice("ch1", "b")
	select {
	case sel := <-ch:
		if len(sel) != 1 || sel[0] != "b" {
			t.Fatalf("got %v", sel)
		}
	case <-time.After(time.Second):
		t.Fatal("choice not resolved")
	}
	if _, _, _, ok := p.HasPendingChoice(); ok {
		t.Fatal("pending choice should be cleared after resolve")
	}
}

// TestWaitExecTimeout 超时后 cleanup 被调用并返回错误。
func TestWaitExecTimeout(t *testing.T) {
	// 临时调低超时便于测试。
	old := PendingTimeout
	PendingTimeout = 200 * time.Millisecond
	defer func() { PendingTimeout = old }()

	p := NewPendingManager()
	ch, cleanup := p.RegisterExec("c2")
	cleaned := false
	origCleanup := cleanup
	cleanup = func() { cleaned = true; origCleanup() }

	done := make(chan struct{})
	go func() {
		_, err := WaitExec(context.Background(), ch, cleanup)
		if err == nil {
			t.Error("expected timeout error")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("WaitExec did not timeout")
	}
	if !cleaned {
		t.Fatal("cleanup should be called on timeout")
	}
}

// TestWaitExecContextCancel ctx 取消时返回错误并清理。
func TestWaitExecContextCancel(t *testing.T) {
	p := NewPendingManager()
	ch, cleanup := p.RegisterExec("c3")
	cleaned := false
	origCleanup := cleanup
	cleanup = func() { cleaned = true; origCleanup() }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, err := WaitExec(ctx, ch, cleanup)
		if err == nil {
			t.Error("expected ctx error")
		}
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("WaitExec did not cancel")
	}
	if !cleaned {
		t.Fatal("cleanup should be called on ctx cancel")
	}
}

// TestSanitizeFilename 路径分隔符与危险字符被替换。
func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"a.png":       "a.png",
		"../evil.png": "evil.png", // filepath.Base 去掉 ..
		"a/b.png":     "b.png",
		`a\b:c*.png`:  "a_b_c_.png",
		"":            "attachment",
		"..":          "attachment",
		"normal file": "normal file",
	}
	for in, want := range cases {
		if got := SanitizeFilename(in); got != want {
			t.Errorf("SanitizeFilename(%q)=%q want %q", in, got, want)
		}
	}
}

// TestChannelStatus 验证 /status 回复包含关键字段。
func TestChannelStatus(t *testing.T) {
	// 无会话管理器：显示无目标提示 + /dir。
	m := NewSessionManagerWithStore("", "", nil)
	s := ChannelStatus(m, Config{}, "telegram", SessionKeyFor("telegram", "1"))
	if s == "" {
		t.Fatal("empty status")
	}
	if !contains(s, "工作空间") {
		t.Errorf("status missing workspace info: %s", s)
	}
	if !contains(s, "/dir") {
		t.Errorf("status missing /dir hint: %s", s)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
