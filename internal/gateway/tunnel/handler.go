package tunnel

import (
	"crypto/subtle"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"cata/internal/cata/clock"
	"cata/internal/cata/tunnel"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  64 * 1024,
	WriteBufferSize: 64 * 1024,
	// 跨域：网关可能由 nginx/caddy 反代，来源不确定；鉴权靠 Bearer token。
	CheckOrigin: func(r *http.Request) bool { return true },
}

// HandlerOptions 隧道端点配置。
type HandlerOptions struct {
	// Token 共享 Bearer token（v1）。空 = 拒绝所有（必须显式配置）。
	Token string
	// AllowAgentIDs 白名单；空 = 放行所有（仍要求 token）。
	AllowAgentIDs []string
}

// Handler 返回 /cata/v1/tunnel 端点处理器。
func Handler(reg *Registry, opts HandlerOptions) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "websocket required", http.StatusBadRequest)
			return
		}
		auth := r.Header.Get("Authorization")
		if !validToken(auth, opts.Token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		agentID := strings.TrimSpace(r.URL.Query().Get("agent"))
		if agentID == "" {
			http.Error(w, "agent param required", http.StatusBadRequest)
			return
		}
		if !allowedAgent(agentID, opts.AllowAgentIDs) {
			http.Error(w, "agent not allowed", http.StatusForbidden)
			return
		}

		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("cata-gateway: tunnel upgrade: %v", err)
			return
		}

		ac := newAgentConn(ws, AgentInfo{
			AgentID:     agentID,
			ConnectedAt: clock.RFC3339(),
			RemoteAddr:  r.RemoteAddr,
		})

		// 第一个帧必须是 hello（含 name/root_path/protocol）。
		ws.SetReadLimit(int64(tunnel.MaxFrameBytes))
		_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
		var hello tunnel.Frame
		if err := ws.ReadJSON(&hello); err != nil {
			_ = ws.Close()
			return
		}
		if hello.Type != tunnel.FrameHello {
			_ = ws.WriteJSON(tunnel.Frame{Type: tunnel.FrameError, Message: "first frame must be hello"})
			_ = ws.Close()
			return
		}
		if hello.Protocol != tunnel.ProtocolName || hello.Version > tunnel.Version {
			_ = ws.WriteJSON(tunnel.Frame{Type: tunnel.FrameError, Message: "protocol mismatch"})
			_ = ws.Close()
			return
		}
		ac.info.Name = hello.Name
		ac.info.RootPath = hello.RootPath
		ac.info.MachineID = hello.MachineID
		ac.info.Protocol = hello.Protocol
		ac.info.Version = hello.Version

		// hello 校验通过后清除握手读超时，避免隧道 10s 后必然断线。
		_ = ws.SetReadDeadline(time.Time{})

		if err := reg.registerConn(ac); err != nil {
			_ = ws.WriteJSON(tunnel.Frame{Type: tunnel.FrameError, Message: err.Error()})
			_ = ws.Close()
			return
		}
		defer reg.Unregister(agentID)
		log.Printf("cata-gateway: agent online: %s (name=%q root=%q addr=%s)", agentID, hello.Name, hello.RootPath, r.RemoteAddr)
		// 网关侧心跳：周期 ping + 读 deadline，检测 NAT/静默断网的半开连接，
		// 让陈旧注册自愈（worker 重连顶替、worker 侧无感知时可被网关踢掉）。
		ac.startHeartbeat(r.Context(), tunnel.HeartbeatInterval)
		ac.readLoop()
		log.Printf("cata-gateway: agent offline: %s", agentID)
	})
}

func validToken(auth, want string) bool {
	if want == "" {
		return false
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	got := strings.TrimSpace(strings.TrimPrefix(auth, prefix))
	if len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func allowedAgent(agentID string, allow []string) bool {
	if len(allow) == 0 {
		return true
	}
	for _, id := range allow {
		if id == agentID {
			return true
		}
	}
	return false
}
