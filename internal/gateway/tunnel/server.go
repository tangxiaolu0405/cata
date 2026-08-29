package tunnel

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"
)

// NewHandler 挂载隧道端点（/cata/v1/tunnel）与 join 流程（/cata/v1/join/challenge|request|status）。
// gateway_token 已移除：join 靠自定义协议头 X-Cata-Join + 挑战-应答（一次性 nonce+签名）区分
// cata 自身报文并自动拦截碰撞/爆破/重放；授权靠一次性 code + 管理员在 UI 批准；
// 隧道握手鉴权用逐机器 token（machine_token，hello 帧）。
func NewHandler(reg *Registry, opts HandlerOptions) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/cata/v1/tunnel", Handler(reg, opts))
	if opts.Join != nil {
		// 层级：协议头拦截（不符 400）→ 限流（防爆破）→ 业务。
		// challenge 端点签发一次性挑战；request 校验挑战（防伪造/重放）。
		mux.Handle("/cata/v1/join/challenge", gateJoinProto(rateLimitJoin(opts.Limiter, JoinChallengeHandler(opts.Join))))
		mux.Handle("/cata/v1/join/request", gateJoinProto(rateLimitJoin(opts.Limiter, gateJoinChallenge(opts.Join, JoinRequestHandler(opts.Join)))))
		mux.Handle("/cata/v1/join/status", gateJoinProto(rateLimitJoin(opts.Limiter, JoinStatusHandler(opts.Join))))
	}
	return mux
}

// JoinChallengeHeaderName 机器回显挑战用的自定义头（随 X-Cata-Join 一起带）。
const (
	JoinChallengeHeaderName    = "X-Cata-Challenge"
	JoinChallengeSigHeaderName = "X-Cata-Challenge-Sig"
)

// JoinChallengeHandler GET /cata/v1/join/challenge → 签发一次性挑战（nonce + HMAC 签名）。
// 机器在 /request 回显；gateway 校验通过才进 join 状态机。60s 有效、一次性。
func JoinChallengeHandler(j *JoinManager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		challenge, sig, err := j.NewChallenge()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]any{"challenge": challenge, "sig": sig})
	})
}

// gateJoinChallenge request 端挑战校验中间件：一次性 nonce+签名不符则 400。
func gateJoinChallenge(j *JoinManager, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := j.VerifyChallenge(
			r.Header.Get(JoinChallengeHeaderName),
			r.Header.Get(JoinChallengeSigHeaderName),
		); err != nil {
			log.Printf("cata-gateway: join challenge invalid from %s (%v): possible forgery/replay", clientIP(r), err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// JoinProtoHeader 自定义握手协议头：标记「cata 自身的 join 报文」，与随机扫描/爆破流量区分。
// 网关端在最外层校验，未携带该头的一律 400 丢弃，从源头降低爆破面。
const (
	JoinProtoHeaderName  = "X-Cata-Join"
	JoinProtoHeaderValue = "cata-tunnel.v1"
)

// gateJoinProto join 端点最外层中间件：校验 X-Cata-Join 协议头。
// 缺失/不符 → 记录 IP 告警并直接 400（不进入限流/状态机）。仅 cata 自身报文带该头。
func gateJoinProto(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(JoinProtoHeaderName) != JoinProtoHeaderValue {
			log.Printf("cata-gateway: join proto mismatch from %s (missing/invalid %s): possible scan/brute-force", clientIP(r), JoinProtoHeaderName)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// rateLimitJoin 对 join 端点套 IP 限流中间件（limiter 为 nil 时透传）。
// 拉黑期间返回 429，带 Retry-After 提示。
func rateLimitJoin(limiter *RateLimiter, next http.Handler) http.Handler {
	if limiter == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if remain := limiter.Allow(clientIP(r)); remain > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(remain.Seconds())+1))
			http.Error(w, "too many requests, retry later", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// JoinRequestHandler POST /cata/v1/join/request → 机器举手，返回一次性 join code。
// 无鉴权：code 本身无权限，还需管理员批准；刷请求只是骚扰（可后加 rate limit）。
func JoinRequestHandler(j *JoinManager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			MachineID string `json:"machine_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		code, err := j.RequestJoin(body.MachineID)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeJSON(w, map[string]any{"join_code": code})
	})
}

// JoinStatusHandler GET /cata/v1/join/status?code=xxx → 机器轮询是否已批准并领取 token。
// 鉴权靠 code 本身（一次性、短时）。
func JoinStatusHandler(j *JoinManager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		st, err := j.Status(code)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		approvedAt := ""
		if !st.ApprovedAt.IsZero() {
			approvedAt = st.ApprovedAt.Format(time.RFC3339)
		}
		writeJSON(w, map[string]any{
			"approved":      st.Approved,
			"machine_token": st.Token,
			"approved_at":   approvedAt,
		})
	})
}

// Run 监听 addr（默认 0.0.0.0:8799）直到 ctx 取消。
// remote 模式下由 cata-gateway 调用：接受各机器 agent 的 WSS 注册并路由。
func Run(ctx context.Context, addr string, reg *Registry, opts HandlerOptions) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           NewHandler(reg, opts),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("cata-gateway: tunnel listening on %s (protocol cata-tunnel.v1)", ln.Addr().String())
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ln)
	}()
	select {
	case <-ctx.Done():
		shCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shCtx)
		return ctx.Err()
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
