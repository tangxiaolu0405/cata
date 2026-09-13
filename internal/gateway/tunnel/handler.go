package tunnel

import (
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
	// Token 网关准入口令（HTTP 握手层；v2 仍作为第一道门）。空 = 拒绝所有（必须显式配置）。
	Token string
	// AllowAgentIDs 白名单；空 = 放行所有（仍要求 token）。
	AllowAgentIDs []string
	// Machines 逐机器 token 表（hello 层校验）；nil = 跳过逐机器校验（兼容本地/测试）。
	Machines *MachinesStore
	// Join join 流程管理器；nil = 不挂载 /cata/v1/join/* 端点。
	Join *JoinManager
	// Limiter join 端点 IP 限流器；nil = 不限流（join 端点本就用 code 一次性保护）。
	Limiter *RateLimiter
}

// Handler 返回 /cata/v1/tunnel 端点处理器。
func Handler(reg *Registry, opts HandlerOptions) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "websocket required", http.StatusBadRequest)
			return
		}
		// 不再预校验 gateway_token：隧道鉴权在 hello 帧用逐机器 token 完成（见下）。
		agentID := strings.TrimSpace(r.URL.Query().Get("agent"))
		machineID := strings.TrimSpace(r.URL.Query().Get("machine"))
		if agentID == "" && machineID == "" {
			http.Error(w, "agent or machine param required", http.StatusBadRequest)
			return
		}
		if agentID != "" && !allowedAgent(agentID, opts.AllowAgentIDs) {
			http.Error(w, "agent not allowed", http.StatusForbidden)
			return
		}

		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("cata-gateway: tunnel upgrade: %v", err)
			return
		}

		if machineID != "" {
			// 机器级隧道（supervisor 持有）：一个入口服务该机器全部注册工作空间。
			serveMachineTunnel(reg, opts, r, ws, machineID)
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

		// hello 层鉴权：per-agent token 优先；缺省回退逐机器 token（兼容旧 worker）。
		// machine 权威的机器首次注册 agent 时按需签发 per-agent token，经 hello_ack 下发。
		if opts.Machines != nil {
			machineOK := opts.Machines.ValidateMachine(hello.MachineID, hello.MachineToken)
			agentOK := strings.TrimSpace(hello.AgentToken) != "" &&
				opts.Machines.ValidateAgent(hello.AgentID, hello.AgentToken)
			if agentOK {
				opts.Machines.TouchSeen(hello.MachineID)
			} else if machineOK {
				opts.Machines.TouchSeen(hello.MachineID)
				// 机器合法但该 agent 尚无 per-agent token：签发（新 token 或已存在）。
				if tok, exists, err := opts.Machines.IssueAgentToken(hello.AgentID, hello.MachineID); err != nil {
					_ = ws.WriteJSON(tunnel.Frame{Type: tunnel.FrameError, Message: "agent token issue failed"})
					_ = ws.Close()
					return
				} else if !exists {
					// 仅当确实新签发才下发；已存在说明 worker 丢了 token，停在这里等它带 token 重连
					// （避免靠 machine token 无限续期）。
					if ackErr := ws.WriteJSON(tunnel.Frame{
						Type: tunnel.FrameHelloAck, AgentID: hello.AgentID, AgentToken: tok,
					}); ackErr != nil {
						_ = ws.Close()
						return
					}
					_ = ws.Close() // 下发后关闭：worker 带 agent_token 重连建立正式隧道
					return
				} else {
					// 已存在但 worker 未带 AgentToken：要求用 per-agent token 重连。
					_ = ws.WriteJSON(tunnel.Frame{Type: tunnel.FrameError, Message: "use agent_token to connect"})
					_ = ws.Close()
					return
				}
			} else {
				_ = ws.WriteJSON(tunnel.Frame{Type: tunnel.FrameError, Message: "machine token invalid"})
				_ = ws.Close()
				return
			}
		}

		// hello 校验通过后清除握手读超时，避免隧道 10s 后必然断线。
		_ = ws.SetReadDeadline(time.Time{})

		if err := reg.registerConn(ac); err != nil {
			_ = ws.WriteJSON(tunnel.Frame{Type: tunnel.FrameError, Message: err.Error()})
			_ = ws.Close()
			return
		}
		// 按连接身份注销：被新连接顶替的旧连接结束时不会误删新注册（见 UnregisterConn）。
		defer reg.UnregisterConn(ac)
		log.Printf("cata-gateway: agent online: %s (name=%q root=%q addr=%s)", agentID, hello.Name, hello.RootPath, r.RemoteAddr)
		// 网关侧心跳：周期 ping + 读 deadline，检测 NAT/静默断网的半开连接，
		// 让陈旧注册自愈（worker 重连顶替、worker 侧无感知时可被网关踢掉）。
		ac.startHeartbeat(r.Context(), tunnel.HeartbeatInterval)
		ac.readLoop()
		log.Printf("cata-gateway: agent offline: %s", agentID)
	})
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

// filterAllowedAgents 过滤机器级隧道发布的工作空间清单（AllowAgentIDs 白名单）。
func filterAllowedAgents(entries []tunnel.AgentEntry, allow []string) []tunnel.AgentEntry {
	if len(allow) == 0 {
		return entries
	}
	out := make([]tunnel.AgentEntry, 0, len(entries))
	for _, e := range entries {
		if allowedAgent(e.AgentID, allow) {
			out = append(out, e)
		}
	}
	return out
}

// serveMachineTunnel 处理**机器级隧道**（由本机 supervisor 持有）的握手与帧循环。
// 与 agent 自持隧道共用同一套 stream 机制（多路复用 + NDJSON 逐字节透传），区别：
//   - hello 带 MachineTunnel=true，用逐机器 token 鉴权（不签发 per-agent token）
//   - hello / agents 帧携带本机工作空间清单：网关据此把「agent 进程已空闲退出」的
//     工作空间仍列为可路由（AgentInfo.Sleeping），收到消息时下发
//     FrameOpen{AgentID: <目标>}，由 supervisor 按需拉起 agent 再转发
func serveMachineTunnel(reg *Registry, opts HandlerOptions, r *http.Request, ws *websocket.Conn, queryMachineID string) {
	ws.SetReadLimit(int64(tunnel.MaxFrameBytes))
	_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	var hello tunnel.Frame
	if err := ws.ReadJSON(&hello); err != nil {
		_ = ws.Close()
		return
	}
	reject := func(msg string) {
		_ = ws.WriteJSON(tunnel.Frame{Type: tunnel.FrameError, Message: msg})
		_ = ws.Close()
	}
	if hello.Type != tunnel.FrameHello {
		reject("first frame must be hello")
		return
	}
	if hello.Protocol != tunnel.ProtocolName || hello.Version > tunnel.Version {
		reject("protocol mismatch")
		return
	}
	if !hello.MachineTunnel {
		reject("machine tunnel hello required (machine_tunnel=true)")
		return
	}
	machineID := strings.TrimSpace(hello.MachineID)
	if machineID == "" || machineID != queryMachineID {
		reject("machine id mismatch (hello vs ?machine=)")
		return
	}
	if opts.Machines != nil {
		if !opts.Machines.ValidateMachine(machineID, hello.MachineToken) {
			reject("machine token invalid")
			return
		}
		opts.Machines.TouchSeen(machineID)
	}

	// hello 校验通过后清除握手读超时，避免隧道 10s 后必然断线。
	_ = ws.SetReadDeadline(time.Time{})

	ac := newAgentConn(ws, AgentInfo{
		AgentID:     "machine:" + machineID,
		Name:        hello.Name,
		MachineID:   machineID,
		Protocol:    hello.Protocol,
		Version:     hello.Version,
		ConnectedAt: clock.RFC3339(),
		RemoteAddr:  r.RemoteAddr,
	})
	// 机器级 stream 要等 supervisor 按需拉起 agent（冷启动），等待上限放宽。
	ac.openTimeout = hubStreamOpenTimeout

	entries := filterAllowedAgents(hello.Agents, opts.AllowAgentIDs)
	reg.RegisterHub(ac, machineID, entries)
	defer reg.UnregisterHub(ac, machineID)
	log.Printf("cata-gateway: machine tunnel online: %s (workspaces=%d addr=%s)", machineID, len(entries), r.RemoteAddr)
	ac.startHeartbeat(r.Context(), tunnel.HeartbeatInterval)
	ac.readLoop()
	log.Printf("cata-gateway: machine tunnel offline: %s", machineID)
}
