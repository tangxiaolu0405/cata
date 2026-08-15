// Package tunnel 内存态 IP 限流 + 拉黑池（join 端点防高频刷）。
//
// 拉黑池是 60~120 秒的临时态，内存即可；网关重启后清空无妨——攻击者继续刷会
// 立即再次被拉黑，持久化收益≈0（因此不引入 sqlite）。
package tunnel

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RateLimitConfig 限流参数。
type RateLimitConfig struct {
	// Window 滑动窗口时长（计数周期）。
	Window time.Duration
	// MaxHits 窗口内允许的最大请求数；超过即拉黑。
	MaxHits int
	// BanDuration 拉黑时长；到期自动放出。
	BanDuration time.Duration
}

// DefaultRateLimitConfig join 端点默认限流：60s 内最多 10 次，超限拉黑 60s。
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		Window:      60 * time.Second,
		MaxHits:     10,
		BanDuration: 60 * time.Second,
	}
}

// RateLimiter 线程安全的内存态限流器。
type RateLimiter struct {
	cfg   RateLimitConfig
	mu    sync.Mutex
	hits  map[string][]time.Time // ip → 窗口内命中时间戳
	banned map[string]time.Time  // ip → 解除拉黑时间
}

// NewRateLimiter 创建限流器；cfg 零值字段用默认值。
func NewRateLimiter(cfg RateLimitConfig) *RateLimiter {
	if cfg.Window <= 0 {
		cfg.Window = 60 * time.Second
	}
	if cfg.MaxHits <= 0 {
		cfg.MaxHits = 10
	}
	if cfg.BanDuration <= 0 {
		cfg.BanDuration = 60 * time.Second
	}
	return &RateLimiter{
		cfg:    cfg,
		hits:   map[string][]time.Time{},
		banned: map[string]time.Time{},
	}
}

// Allow 判断某 IP 是否放行；返回剩余拉黑时长（0 = 放行）。
// 拉黑期间不计数、不放行；窗口内超阈值则拉黑。
func (r *RateLimiter) Allow(ip string) time.Duration {
	ip = normalizeIP(ip)
	if ip == "" {
		return 0
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()

	// 拉黑检查。
	if until, ok := r.banned[ip]; ok {
		if now.Before(until) {
			return until.Sub(now)
		}
		delete(r.banned, ip)
	}

	// 滑动窗口：丢弃过期命中。
	cutoff := now.Add(-r.cfg.Window)
	kept := r.hits[ip][:0]
	for _, t := range r.hits[ip] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	r.hits[ip] = kept

	if len(kept) > r.cfg.MaxHits {
		delete(r.hits, ip)
		r.banned[ip] = now.Add(r.cfg.BanDuration)
		return r.cfg.BanDuration
	}
	return 0
}

// IsBanned 某 IP 是否在拉黑池中（返回剩余时长；0 = 未拉黑）。
func (r *RateLimiter) IsBanned(ip string) time.Duration {
	ip = normalizeIP(ip)
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if until, ok := r.banned[ip]; ok && now.Before(until) {
		return until.Sub(now)
	}
	return 0
}

// BanCount 当前拉黑池大小（测试/观测用）。
func (r *RateLimiter) BanCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.banned)
}

// normalizeIP 去掉端口、trim；空返回 ""。
func normalizeIP(ip string) string {
	ip = strings.TrimSpace(ip)
	if host, _, err := net.SplitHostPort(ip); err == nil {
		return strings.TrimSpace(host)
	}
	return ip
}

// clientIP 从请求提取客户端 IP：优先 X-Forwarded-For 首个，回退 RemoteAddr。
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			if ip := normalizeIP(parts[0]); ip != "" {
				return ip
			}
		}
	}
	return normalizeIP(r.RemoteAddr)
}
