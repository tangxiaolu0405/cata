package gateway

import (
	"log"
	"net"
	"time"

	"cata/internal/cata/config"
	"cata/internal/cata/link"
)

// DialLocalAgent 返回拨「本机某工作空间 agent 的 per-ws socket」的 dialer。
// agentID = ws_id：本地 UI 项目列表的 project.ID 即 ws_id，与 agent 一一对应。
// EnsureAgent 幂等拉起该工作空间的 agent 进程（`cata agent --workspace <id>`），
// 未注册的项目按需拉起、空闲回收，因此本地多空间不再依赖 legacy cata.sock。
//
// 与 remote 模式的 reg.DialAgent 对称：两者都返回一个拨「对应 agent」的 dialer，
// 只是本地拨 Unix socket、remote 拨 WSS 隧道——上层 session/UI 代码完全无感。
func DialLocalAgent(agentID string) func() (net.Conn, error) {
	return func() (net.Conn, error) {
		if err := link.EnsureAgent(agentID); err != nil {
			return nil, err
		}
		return net.DialTimeout("unix", config.ResolvedAgentSocketPath(agentID), 5*time.Second)
	}
}

// EnsureLocalAgents 本地模式的启动保障：确保 supervisor 守护在跑，以保活 link.json
// 里注册的常驻（keep-alive）agent。未注册项目的 agent 由 DialLocalAgent 按需拉起，
// 无需在此预启动。autoStart=false（channel/external）时跳过——用户自管进程。
func EnsureLocalAgents(cfg Config) {
	if !cfg.ShouldAutoStartServer() {
		return
	}
	if err := link.EnsureSupervisorDaemon(); err != nil {
		log.Printf("cata-gateway: ensure supervisor: %v", err)
		return
	}
	log.Printf("cata-gateway: supervisor running (keep-alive agents will be ensured)")
}
