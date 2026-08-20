package ui

import (
	"testing"
	"time"
)

func TestIPBanTriggerAndReset(t *testing.T) {
	b := newIPBan(3, 10*time.Minute)

	// 2 次失败：未封禁。
	if banned, _ := b.recordFailure("1.2.3.4"); banned {
		t.Fatal("should not ban after 1st failure")
	}
	if banned, _ := b.recordFailure("1.2.3.4"); banned {
		t.Fatal("should not ban after 2nd failure")
	}
	if rem := b.bannedRemaining("1.2.3.4"); rem != 0 {
		t.Fatalf("expected not banned, got %v", rem)
	}

	// 第 3 次：触发封禁。
	if banned, rem := b.recordFailure("1.2.3.4"); !banned || rem <= 0 {
		t.Fatalf("expected ban, got banned=%v rem=%v", banned, rem)
	}
	if rem := b.bannedRemaining("1.2.3.4"); rem <= 0 {
		t.Fatalf("expected banned remaining, got %v", rem)
	}

	// 封禁期间继续失败不会改变解封时间（仍被封）。
	if banned, _ := b.recordFailure("1.2.3.4"); !banned {
		t.Fatal("should stay banned")
	}

	// 成功登录清零失败计数；封禁本身保留到到期（本测试不演进时间，故仍封）。
	b.recordSuccess("1.2.3.4")
	if rem := b.bannedRemaining("1.2.3.4"); rem <= 0 {
		t.Fatal("ban persists until expiry even after success")
	}
}

func TestIPBanExpiry(t *testing.T) {
	b := newIPBan(2, 5*time.Millisecond)
	b.recordFailure("9.9.9.9")
	b.recordFailure("9.9.9.9") // 触发封禁
	if rem := b.bannedRemaining("9.9.9.9"); rem <= 0 {
		t.Fatal("expected banned")
	}
	time.Sleep(10 * time.Millisecond)
	if rem := b.bannedRemaining("9.9.9.9"); rem != 0 {
		t.Fatalf("expected ban expired, got %v", rem)
	}
}

func TestSessionStoreValid(t *testing.T) {
	s := newSessionStore("secret", time.Hour)
	tok, ok := s.create("secret")
	if !ok || tok == "" {
		t.Fatal("create should succeed with correct password")
	}
	if !s.valid(tok) {
		t.Fatal("session should be valid right after create")
	}
	if s.valid("wrong-token") {
		t.Fatal("unknown token must be invalid")
	}
	// 错误口令不签发。
	if _, ok := s.create("wrong"); ok {
		t.Fatal("create must fail with wrong password")
	}
	s.destroy(tok)
	if s.valid(tok) {
		t.Fatal("session invalid after destroy")
	}
}

func TestStripPort(t *testing.T) {
	cases := map[string]string{
		"1.2.3.4:8787": "1.2.3.4",
		"::1":          "::1",
		"2001:db8::1":  "2001:db8::1",
	}
	for in, want := range cases {
		if got := stripPort(in); got != want {
			t.Fatalf("stripPort(%q)=%q want %q", in, got, want)
		}
	}
}
