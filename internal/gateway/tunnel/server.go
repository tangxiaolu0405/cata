package tunnel

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"time"
)

// NewHandler 挂载隧道端点（/cata/v1/tunnel）与在线 agent API（/cata/v1/agents）。
func NewHandler(reg *Registry, opts HandlerOptions) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/cata/v1/tunnel", Handler(reg, opts))
	mux.Handle("/cata/v1/agents", AgentsHandler(reg))
	return mux
}

// AgentsHandler GET /cata/v1/agents → 在线 agent 列表 JSON（Web UI 远程项目列表用）。
func AgentsHandler(reg *Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
