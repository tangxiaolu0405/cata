package link

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"cata/internal/cata/tunnel"
)

// 机器级隧道（cata-tunnel.v1 的 machine_tunnel 模式）：由**本机 supervisor** 持有，
// 每机器一条 WSS，代替「每个 agent 各持一条」。
//
// 为什么：agent 在半小时无消息后空闲退出（省内存/省 LLM 常驻），但云端 gateway 必须
// 有个常驻入口才能把消息送下来。supervisor 常驻并持有这条隧道，gateway 要某工作空间的
// 连接时会下发 FrameOpen{AgentID: <目标>}，supervisor 侧**按需拉起该 agent 进程**、
// 拨其 per-ws socket，再把 stream 逐字节转发过去——首次消息有冷启动延迟，
// 之后按 agent 自身的空闲回收节奏休眠。
//
// 机器身份/鉴权走 join 签发的逐机器 token（与 agent 自持隧道同一套 MachinesStore）。

// RunSupervisorTunnel 常驻运行机器级隧道（断线自动重连，退避 1s → 30s）。
// 尚未配网关（还没 `cata link join`）不算错误：每 30s 重读 link.json，配置出现即接入——
// 这样「先起 supervisor、后 join」的流程无需重启守护。
// ctx 取消（supervisor 退出）时返回 nil。
func RunSupervisorTunnel(ctx context.Context, backoff *ensureBackoff) error {
	if backoff == nil {
		backoff = newEnsureBackoff()
	}
	hub := &machineHub{backoff: backoff, denied: map[string]bool{}}
	delay := time.Second
	notConfiguredLogged := false
	for {
		cfg, err := LoadConfig()
		if err != nil {
			log.Printf("cata supervisor: machine tunnel: load link.json: %v (retry in %s)", err, delay)
			if !sleepCtx(ctx, delay) {
				return nil
			}
			continue
		}
		if !cfg.GatewayConfigured() {
			if !notConfiguredLogged {
				log.Println("cata supervisor: machine tunnel: gateway not configured — waiting for `cata link join`")
				notConfiguredLogged = true
			}
			if !sleepCtx(ctx, 30*time.Second) {
				return nil
			}
			continue
		}
		notConfiguredLogged = false
		err = hub.runOnce(ctx, cfg)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			log.Printf("cata supervisor: machine tunnel: %v (reconnect in %s)", err, delay)
		}
		if !sleepCtx(ctx, delay) {
			return nil
		}
		if delay < 30*time.Second {
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
}

// sleepCtx 等待 d 或 ctx 取消；返回 false 表示 ctx 已取消。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// machineHub 机器级隧道的进程内状态（跨重连保留：吊销记录、最近下发的清单）。
type machineHub struct {
	backoff *ensureBackoff

	mu      sync.Mutex
	denied  map[string]bool     // gateway 下发 revoke 的工作空间（本进程生命周期内不再服务）
	entries []tunnel.AgentEntry // 最近一次下发的清单（变化才推送）
}

// runOnce 建立一条机器级隧道并服务到断开。
func (h *machineHub) runOnce(ctx context.Context, cfg Config) error {
	wsURL, err := machineTunnelWSURL(cfg.GatewayURL, cfg.MachineID)
	if err != nil {
		return err
	}
	if strings.HasPrefix(wsURL, "ws://") {
		log.Printf("cata supervisor: WARNING: gateway url is ws:// (token sent in plaintext); use wss:// in production")
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+cfg.MachineToken)

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, resp, err := dialer.Dial(wsURL, header)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("dial %s: %s (%s)", wsURL, err, resp.Status)
		}
		return fmt.Errorf("dial %s: %w", wsURL, err)
	}
	defer conn.Close()

	entries := h.listEntries(cfg)
	h.setEntries(entries)

	hello := tunnel.Frame{
		Type:          tunnel.FrameHello,
		MachineTunnel: true,
		MachineID:     cfg.MachineID,
		MachineToken:  cfg.MachineToken,
		Name:          hostName(),
		Agents:        entries,
		Protocol:      tunnel.ProtocolName,
		Version:       tunnel.Version,
	}
	if err := conn.WriteJSON(hello); err != nil {
		return err
	}
	log.Printf("cata supervisor: machine tunnel connected: %s (workspaces=%d)", wsURL, len(entries))

	tc := &tunnelConn{
		conn:       conn,
		streams:    map[uint64]*tunnelStream{},
		errCh:      make(chan error, 1),
		dialSocket: h.ensureAndDial,
	}
	defer tc.closeAll()

	stop := make(chan struct{})
	defer close(stop)
	go h.pushEntriesLoop(ctx, tc, stop)

	conn.SetReadLimit(int64(tunnel.MaxFrameBytes))
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-tc.errCh:
			return err
		default:
		}
		// 网关周期 ping（HeartbeatInterval）；读 deadline 设为 3×，
		// 网关静默消失（NAT 超时等）时能自行感知并重连。
		_ = conn.SetReadDeadline(time.Now().Add(3 * tunnel.HeartbeatInterval))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var f tunnel.Frame
		if err := json.Unmarshal(msg, &f); err != nil {
			log.Printf("cata supervisor: machine tunnel: bad frame: %v", err)
			continue
		}
		if f.Type == tunnel.FrameRevoke {
			h.revoke(f.AgentID)
			continue
		}
		tc.handleFrame(cfg.MachineID, f)
	}
}

// ensureAndDial 机器级隧道的 stream 拨号：先把该工作空间的 agent 按需拉起，
// 再拨它的 per-ws socket。这是「消息到达时 agent 不在就拉起并转发」的落点。
func (h *machineHub) ensureAndDial(agentID string) (net.Conn, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, fmt.Errorf("empty agent id in open frame")
	}
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	if !cfg.HasAgent(agentID) {
		return nil, fmt.Errorf("workspace %s not registered on this machine", agentID)
	}
	if h.isDenied(agentID) {
		return nil, fmt.Errorf("workspace %s revoked", agentID)
	}
	// 带崩溃退避：工作空间已删/启动即崩时不会被无限重生刷日志。
	if err := h.backoff.ensure(agentID); err != nil {
		return nil, err
	}
	return dialAgentSocketDirect(agentID)
}

// listEntries 本机对外发布的工作空间清单（注册 + 启用 + 未被吊销）。
func (h *machineHub) listEntries(cfg Config) []tunnel.AgentEntry {
	h.mu.Lock()
	denied := make(map[string]bool, len(h.denied))
	for id, v := range h.denied {
		denied[id] = v
	}
	h.mu.Unlock()

	out := make([]tunnel.AgentEntry, 0, len(cfg.Agents))
	for _, id := range cfg.LinkedAgentIDs() {
		e := cfg.Agents[id]
		if !e.Enabled || denied[id] {
			continue
		}
		name := strings.TrimSpace(e.Name)
		if name == "" {
			name = id
		}
		out = append(out, tunnel.AgentEntry{AgentID: id, Name: name, RootPath: e.RootPath})
	}
	return out
}

func (h *machineHub) setEntries(entries []tunnel.AgentEntry) {
	h.mu.Lock()
	h.entries = entries
	h.mu.Unlock()
}

func (h *machineHub) isDenied(agentID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.denied[agentID]
}

// revoke 处理 gateway 下发的吊销：本进程生命周期内不再服务该工作空间，并停掉其 agent。
func (h *machineHub) revoke(agentID string) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return
	}
	h.mu.Lock()
	h.denied[agentID] = true
	out := make([]tunnel.AgentEntry, 0, len(h.entries))
	for _, e := range h.entries {
		if e.AgentID != agentID {
			out = append(out, e)
		}
	}
	h.entries = out
	h.mu.Unlock()
	log.Printf("cata supervisor: workspace %s revoked by gateway; stopping agent", agentID)
	if err := killAgentProcess(agentID); err != nil {
		log.Printf("cata supervisor: revoke %s: stop agent: %v", agentID, err)
	}
}

// pushEntriesLoop 周期性复查 link.json，清单变化时推送 agents 帧（新注册/注销的工作空间
// 无需等重连即可被 gateway 路由）。
func (h *machineHub) pushEntriesLoop(ctx context.Context, tc *tunnelConn, stop <-chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			cfg, err := LoadConfig()
			if err != nil {
				continue
			}
			entries := h.listEntries(cfg)
			h.mu.Lock()
			changed := !sameEntries(h.entries, entries)
			if changed {
				h.entries = entries
			}
			h.mu.Unlock()
			if !changed {
				continue
			}
			if err := tc.send(tunnel.Frame{Type: tunnel.FrameAgents, Agents: entries}); err != nil {
				log.Printf("cata supervisor: machine tunnel: push agents: %v", err)
				return
			}
			log.Printf("cata supervisor: machine tunnel: pushed %d workspace(s)", len(entries))
		}
	}
}

func sameEntries(a, b []tunnel.AgentEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// machineTunnelWSURL 机器级隧道的 WSS 端点（?machine=<machine_id>，不带 agent）。
func machineTunnelWSURL(gatewayURL, machineID string) (string, error) {
	u, err := tunnelEndpointURL(gatewayURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("machine", strings.TrimSpace(machineID))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// tunnelEndpointURL 归一化网关 URL 到隧道端点（scheme http(s)→ws(s)，路径 + /cata/v1/tunnel）。
func tunnelEndpointURL(gatewayURL string) (*url.URL, error) {
	gw := strings.TrimSpace(gatewayURL)
	if gw == "" {
		return nil, fmt.Errorf("empty gateway url")
	}
	u, err := url.Parse(normalizeGatewayURL(gw))
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return nil, fmt.Errorf("unsupported gateway url scheme %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/cata/v1/tunnel"
	return u, nil
}

// hostName 本机名（hello 帧展示用）；取不到返回空串。
func hostName() string {
	n, err := os.Hostname()
	if err != nil {
		return ""
	}
	return n
}
