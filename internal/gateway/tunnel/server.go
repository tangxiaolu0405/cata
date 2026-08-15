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

// NewHandler 挂载隧道端点（/cata/v1/tunnel）、在线 agent API（/cata/v1/agents）、
// 与 join 流程（/cata/v1/join/request|status|approve）。
func NewHandler(reg *Registry, opts HandlerOptions) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/cata/v1/tunnel", Handler(reg, opts))
	mux.Handle("/cata/v1/agents", AgentsHandler(reg, opts))
	if opts.Join != nil {
		mux.Handle("/cata/v1/join/request", rateLimitJoin(opts.Limiter, JoinRequestHandler(opts.Join)))
		mux.Handle("/cata/v1/join/status", rateLimitJoin(opts.Limiter, JoinStatusHandler(opts.Join)))
		mux.Handle("/cata/v1/join/approve", JoinApproveHandler(opts.Join, opts))
	}
	return mux
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
		approved, token, err := j.Status(code)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		writeJSON(w, map[string]any{"approved": approved, "machine_token": token})
	})
}

// JoinApproveHandler POST /cata/v1/join/approve → 管理员批准，签发逐机器 token。
// 管理动作，要求 gateway_token 鉴权（与隧道同口令），或由 UI 内部（LAN）调用。
func JoinApproveHandler(j *JoinManager, opts HandlerOptions) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !validToken(r.Header.Get("Authorization"), opts.Token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		machineID, err := j.ApproveJoin(body.Code)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "machine_id": machineID})
	})
}

// AgentsHandler GET /cata/v1/agents → 在线 agent 列表 JSON（Web UI 远程项目列表用）。
// 与隧道端点共用 Bearer token 鉴权：泄露 agent 列表会暴露各项目路径与拓扑。
func AgentsHandler(reg *Registry, opts HandlerOptions) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !validToken(r.Header.Get("Authorization"), opts.Token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		writeJSON(w, map[string]any{"agents": reg.OnlineAgents()})
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
