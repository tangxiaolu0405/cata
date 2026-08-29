package ui

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// 登录页 + 连续失败封 IP 防护（仅当配置了 ui_password 时启用）。
//
// 设计要点：
//   - 无 ui_password：保持原有 LAN-only 行为（公网直连拒绝），不弹登录页。
//   - 有 ui_password：放开公网，但所有请求需经登录会话 cookie；未登录跳 /login。
//     连续 LoginBanMaxAttempts 次失败封该 IP LoginBanDuration（默认 10 分钟）。
//   - 封禁与失败计数均内存态（重启清空无妨，攻击者继续刷会立即再被封）。

const (
	// SessionCookieName 登录会话 cookie 名。
	SessionCookieName = "cata_gw_sid"
	// LoginPath 登录页与登录接口路径。
	LoginPath    = "/login"
	LoginAPIPath = "/api/login"
)

// LoginBanMaxAttemptsDefault 默认连续失败封禁阈值。
const LoginBanMaxAttemptsDefault = 5

// LoginBanDurationDefault 默认封禁时长。
const LoginBanDurationDefault = 10 * time.Minute

// ipBan 按 IP 记录连续登录失败并封禁。
type ipBan struct {
	mu       sync.Mutex
	max      int
	dur      time.Duration
	fails    map[string]int       // ip → 连续失败计数
	bannedAt map[string]time.Time // ip → 解封时间
}

func newIPBan(max int, dur time.Duration) *ipBan {
	if max <= 0 {
		max = LoginBanMaxAttemptsDefault
	}
	if dur <= 0 {
		dur = LoginBanDurationDefault
	}
	return &ipBan{
		max:      max,
		dur:      dur,
		fails:    map[string]int{},
		bannedAt: map[string]time.Time{},
	}
}

// recordFailure 记录一次失败；达到阈值即封禁，返回当前是否已被封（含本次触发）。
func (b *ipBan) recordFailure(ip string) (banned bool, remaining time.Duration) {
	ip = normalizeUIClientIP(ip)
	if ip == "" {
		return false, 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if until, ok := b.bannedAt[ip]; ok && time.Now().Before(until) {
		return true, until.Sub(time.Now())
	}
	b.fails[ip]++
	if b.fails[ip] >= b.max {
		delete(b.fails, ip)
		b.bannedAt[ip] = time.Now().Add(b.dur)
		return true, b.dur
	}
	return false, 0
}

// recordSuccess 登录成功：清零失败计数（若已封则保留到到期）。
func (b *ipBan) recordSuccess(ip string) {
	ip = normalizeUIClientIP(ip)
	if ip == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.fails, ip)
}

// bannedRemaining 返回该 IP 剩余封禁时长（0 = 未封禁）。
func (b *ipBan) bannedRemaining(ip string) time.Duration {
	ip = normalizeUIClientIP(ip)
	if ip == "" {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if until, ok := b.bannedAt[ip]; ok {
		if time.Now().Before(until) {
			return until.Sub(time.Now())
		}
		delete(b.bannedAt, ip)
	}
	return 0
}

// sessionStore 内存态登录会话（cookie → 上次活动时间）。
// 口令支持两种形态：
//   - bcrypt hash（以 $2 开头）：推荐，配置里存 hash 而非明文（用 `cata-gateway passwd` 生成）；
//   - 明文（无前缀）：向后兼容旧配置，校验用常量时间比较，并打一次警告建议换 hash。
//
// 会话滑动过期：每次有效访问刷新 lastSeen，连续 idle 超过 maxAge 才失效。
type sessionStore struct {
	mu       sync.RWMutex
	entries  map[string]time.Time // token → lastSeen
	maxAge   time.Duration
	password string // bcrypt hash 或明文（旧配置）
}

func newSessionStore(password string, maxAge time.Duration) *sessionStore {
	if maxAge <= 0 {
		maxAge = 24 * time.Hour
	}
	if isPlainPassword(password) {
		log.Printf("cata-gateway: ui_password 为明文存储，建议改用 bcrypt hash（cata-gateway passwd 生成后写入 ui_password）")
	}
	return &sessionStore{
		entries:  map[string]time.Time{},
		maxAge:   maxAge,
		password: password,
	}
}

// isPlainPassword 判断口令是否非 bcrypt hash（bcrypt hash 固定以 $2 开头）。
func isPlainPassword(pw string) bool {
	pw = strings.TrimSpace(pw)
	return pw == "" || !(strings.HasPrefix(pw, "$2a$") || strings.HasPrefix(pw, "$2b$") || strings.HasPrefix(pw, "$2y$"))
}

// verifyPassword 校验用户输入口令：bcrypt hash 用 bcrypt.Compare，明文用常量时间比较。
func (s *sessionStore) verifyPassword(input string) bool {
	if s.password == "" {
		return false
	}
	input = strings.TrimSpace(input)
	if isPlainPassword(s.password) {
		return subtle.ConstantTimeCompare([]byte(s.password), []byte(input)) == 1
	}
	if err := bcrypt.CompareHashAndPassword([]byte(s.password), []byte(input)); err != nil {
		return false
	}
	return true
}

// create 校验口令并生成会话 token。
func (s *sessionStore) create(password string) (token string, ok bool) {
	if !s.verifyPassword(password) {
		return "", false
	}
	token = randSessionToken()
	s.mu.Lock()
	s.entries[token] = time.Now()
	s.mu.Unlock()
	return token, true
}

// valid 校验会话 cookie 是否仍有效（滑动过期：有效访问刷新 lastSeen）。
func (s *sessionStore) valid(token string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lastSeen, ok := s.entries[token]
	if !ok {
		return false
	}
	if time.Since(lastSeen) > s.maxAge {
		delete(s.entries, token)
		return false
	}
	// 滑动续期：活跃会话不因绝对时间过期，只有连续闲置超时才失效。
	s.entries[token] = time.Now()
	return true
}

// destroy 注销会话。
func (s *sessionStore) destroy(token string) {
	if token == "" {
		return
	}
	s.mu.Lock()
	delete(s.entries, token)
	s.mu.Unlock()
}

func randSessionToken() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// isSecureRequest 判断请求是否走 TLS（或经反代以 https 转发）：用于会话 cookie 的 Secure 属性。
// 未启用 HTTPS 时不标记 Secure，避免纯 http（局域网）场景 cookie 被浏览器拒收导致无法登录。
func isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))) {
	case "https", "wss":
		return true
	}
	return false
}

// normalizeUIClientIP 对传入 IP 字符串去端口、trim，供 ipBan 计数用。
func normalizeUIClientIP(ip string) string {
	return stripPort(ip)
}

func clientIPFromRequest(r *http.Request) string {
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[0]); ip != "" {
			return stripPort(ip)
		}
	}
	return stripPort(r.RemoteAddr)
}

func stripPort(hostport string) string {
	hostport = strings.TrimSpace(hostport)
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return strings.TrimSpace(host)
	}
	return hostport
}

// isLoginRoute 登录页 / 登录接口免鉴权。
func isLoginRoute(path string) bool {
	return path == LoginPath || path == LoginAPIPath
}

// serveLoginResult 向未登录请求返回 401（API）或重定向到登录页（页面）。
func serveLoginResult(w http.ResponseWriter, r *http.Request) {
	if wantsJSON(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, LoginPath, http.StatusFound)
}

func wantsJSON(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") {
		return true
	}
	return strings.HasPrefix(r.URL.Path, "/api/")
}
