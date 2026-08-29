package ui

import (
	"crypto/tls"
	"net/http"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
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

// TestSessionStoreBcrypt 验证 ui_password 存 bcrypt hash 时校验正确、明文口令可登录。
func TestSessionStoreBcrypt(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	s := newSessionStore(string(hash), time.Hour)
	if isPlainPassword(string(hash)) {
		t.Fatal("bcrypt hash should not be detected as plain")
	}
	if _, ok := s.create("secret"); !ok {
		t.Fatal("bcrypt store should accept correct plaintext")
	}
	if _, ok := s.create("wrong"); ok {
		t.Fatal("bcrypt store must reject wrong password")
	}
}

// TestSessionSlidingExpiry 验证滑动过期：活跃访问刷新 lastSeen，闲置超时才失效。
func TestSessionSlidingExpiry(t *testing.T) {
	s := newSessionStore("secret", 100*time.Millisecond)
	tok, ok := s.create("secret")
	if !ok {
		t.Fatal("create failed")
	}
	time.Sleep(60 * time.Millisecond)
	if !s.valid(tok) {
		t.Fatal("should still be valid before idle timeout")
	}
	// 距上次访问不到 100ms，应持续有效（滑动续期）。
	time.Sleep(60 * time.Millisecond)
	if !s.valid(tok) {
		t.Fatal("sliding window should keep session alive while active")
	}
	// 闲置超过 window 后失效。
	time.Sleep(150 * time.Millisecond)
	if s.valid(tok) {
		t.Fatal("session should expire after idle timeout")
	}
}

// TestIsSecureRequest 验证 X-Forwarded-Proto=https 与 TLS 判定。
func TestIsSecureRequest(t *testing.T) {
	if isSecureRequest(&http.Request{}) {
		t.Fatal("plain http should not be secure")
	}
	r := &http.Request{Header: http.Header{}}
	r.Header.Set("X-Forwarded-Proto", "https")
	if !isSecureRequest(r) {
		t.Fatal("X-Forwarded-Proto=https should be secure")
	}
	r2 := &http.Request{TLS: &tls.ConnectionState{}}
	if !isSecureRequest(r2) {
		t.Fatal("TLS request should be secure")
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
