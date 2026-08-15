package tunnel

import (
	"net/http/httptest"
	"testing"
	"time"
)

// TestRateLimiterBanOnHighFreq 高频请求触发拉黑，拉黑期间拒绝，到期放出。
func TestRateLimiterBanOnHighFreq(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		Window:      time.Second,
		MaxHits:     3,
		BanDuration: 60 * time.Millisecond,
	})

	// 窗口内前 3 次放行。
	for i := 0; i < 3; i++ {
		if rl.Allow("1.2.3.4") != 0 {
			t.Fatalf("hit %d should be allowed", i)
		}
	}
	// 第 4 次超阈值，触发拉黑。
	if remain := rl.Allow("1.2.3.4"); remain <= 0 {
		t.Fatal("4th hit should trigger ban")
	}
	if rl.IsBanned("1.2.3.4") <= 0 {
		t.Fatal("IP should be banned")
	}
	if rl.BanCount() != 1 {
		t.Fatalf("BanCount=%d want 1", rl.BanCount())
	}
	// 拉黑期间持续拒绝。
	if rl.Allow("1.2.3.4") <= 0 {
		t.Fatal("banned IP should be rejected")
	}

	// 到期放出。
	time.Sleep(80 * time.Millisecond)
	if rl.IsBanned("1.2.3.4") != 0 {
		t.Fatal("IP should be released after ban duration")
	}
	if rl.Allow("1.2.3.4") != 0 {
		t.Fatal("released IP should be allowed again")
	}
}

// TestRateLimiterIsolateIPs 不同 IP 互不影响。
func TestRateLimiterIsolateIPs(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		Window:      time.Second,
		MaxHits:     2,
		BanDuration: 60 * time.Millisecond,
	})
	// IP A 刷到拉黑。
	rl.Allow("1.1.1.1")
	rl.Allow("1.1.1.1")
	rl.Allow("1.1.1.1")
	if rl.IsBanned("1.1.1.1") <= 0 {
		t.Fatal("A should be banned")
	}
	// IP B 不受影响。
	if rl.Allow("2.2.2.2") != 0 {
		t.Fatal("B should be allowed")
	}
}

// TestRateLimiterWindowSlides 滑动窗口：窗口过后计数重置，不误伤。
func TestRateLimiterWindowSlides(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		Window:      30 * time.Millisecond,
		MaxHits:     2,
		BanDuration: 60 * time.Millisecond,
	})
	rl.Allow("1.1.1.1")
	rl.Allow("1.1.1.1")
	// 等窗口滑过，计数应重置。
	time.Sleep(50 * time.Millisecond)
	if rl.Allow("1.1.1.1") != 0 {
		t.Fatal("after window slides, should be allowed")
	}
	if rl.Allow("1.1.1.1") != 0 {
		t.Fatal("second hit in new window should be allowed")
	}
}

// TestClientIP 提取客户端 IP：X-Forwarded-For 优先，回退 RemoteAddr。
func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "9.9.9.9:12345"
	if got := clientIP(r); got != "9.9.9.9" {
		t.Fatalf("RemoteAddr fallback: got %q", got)
	}
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "10.0.0.1:12345"
	r2.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	if got := clientIP(r2); got != "1.2.3.4" {
		t.Fatalf("XFF first: got %q", got)
	}
}
