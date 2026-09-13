// Package tunnel 网关侧 cata-tunnel.v1 服务端：接受各机器 agent 进程的 WSS 注册，
// 把 Web UI / 渠道会话变成多条 stream 反向拨到对应 agent 的本地 per-ws socket。
package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"cata/internal/cata/tunnel"
)

// AgentInfo 在线 agent 的注册信息（供 Web UI / 渠道路由）。
type AgentInfo struct {
	AgentID     string `json:"agent_id"`
	Name        string `json:"name"`
	RootPath    string `json:"root_path,omitempty"`
	MachineID   string `json:"machine_id,omitempty"` // 机器标识（hello 帧携带），用于按机器分组/路由 register
	Protocol    string `json:"protocol,omitempty"`
	Version     int    `json:"version,omitempty"`
	ConnectedAt string `json:"connected_at,omitempty"`
	RemoteAddr  string `json:"remote_addr,omitempty"`
	// Sleeping true = 该工作空间由**机器级隧道**（supervisor）发布，本机 agent 进程
	// 当前不在线（无消息空闲已退出）。网关仍可路由：首次消息会经机器级隧道
	// 触发 supervisor 按需拉起该 agent 再转发。
	Sleeping bool `json:"sleeping,omitempty"`
}

// Registry 在线 agent 注册中心（无状态：网关重启后 agent 重连即恢复）。
//
// 两类入口：
//   - agents：单个 agent 进程自持的隧道（legacy；一个 agent 一条 WSS）
//   - hubs：**机器级隧道**（每机器一条，由 supervisor 持有），一个入口服务该机器
//     全部注册工作空间，按需唤醒对应 agent 进程
type Registry struct {
	mu        sync.Mutex
	agents    map[string]*agentConn
	hubs      map[string]*agentConn          // machine_id → 机器级隧道连接
	hubAgents map[string][]tunnel.AgentEntry // machine_id → 该机器发布的工作空间清单
}

// NewRegistry 创建注册中心。
func NewRegistry() *Registry {
	return &Registry{
		agents:    map[string]*agentConn{},
		hubs:      map[string]*agentConn{},
		hubAgents: map[string][]tunnel.AgentEntry{},
	}
}

// registerConn 注册一个 agent 的 WSS 连接。agent_id 已在线时，若旧连接是陈旧的
// （半开/静默断线）则由新连接顶替：先关闭旧连接再注册新连接，避免 worker 重连被拒
// 形成「无限重连被拒」死锁。
func (r *Registry) registerConn(a *agentConn) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.agents[a.info.AgentID]; ok {
		log.Printf("registry: agent %q re-registering; replacing stale connection", a.info.AgentID)
		old.closeFromGateway()
	}
	r.agents[a.info.AgentID] = a
	return nil
}

// RegisterHub 注册一条**机器级隧道**（supervisor 持有）：该机器全部注册工作空间
// 经此连接路由，agent 进程不在线时由 supervisor 按需拉起。
// 同一机器重复 hello（worker 重连）时顶替旧连接。
func (r *Registry) RegisterHub(a *agentConn, machineID string, agents []tunnel.AgentEntry) {
	machineID = strings.TrimSpace(machineID)
	r.mu.Lock()
	if old, ok := r.hubs[machineID]; ok && old != a {
		log.Printf("registry: machine %q re-registering tunnel; replacing stale connection", machineID)
		old.closeFromGateway()
	}
	r.hubs[machineID] = a
	r.hubAgents[machineID] = agents
	a.onAgents = func(entries []tunnel.AgentEntry) { r.UpdateHubAgents(machineID, a, entries) }
	r.mu.Unlock()
}

// UnregisterHub 移除机器级隧道（仅当注册表里该机器仍指向本连接时）。
func (r *Registry) UnregisterHub(a *agentConn, machineID string) {
	machineID = strings.TrimSpace(machineID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.hubs[machineID]; ok && cur == a {
		a.closeAllStreams()
		delete(r.hubs, machineID)
		delete(r.hubAgents, machineID)
	}
}

// UpdateHubAgents 机器级隧道推送最新工作空间清单（link.json 变化时）。
func (r *Registry) UpdateHubAgents(machineID string, a *agentConn, agents []tunnel.AgentEntry) {
	machineID = strings.TrimSpace(machineID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.hubs[machineID]; !ok || cur != a {
		return // 连接已被顶替/注销：忽略陈旧推送
	}
	r.hubAgents[machineID] = agents
}

// dropHubAgent 从机器级隧道发布清单里剔除某工作空间（吊销时立即停止路由）。
func (r *Registry) dropHubAgent(agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for machineID, entries := range r.hubAgents {
		out := make([]tunnel.AgentEntry, 0, len(entries))
		for _, e := range entries {
			if e.AgentID == agentID {
				continue
			}
			out = append(out, e)
		}
		r.hubAgents[machineID] = out
	}
}

// UnregisterConn 移除 agent 连接（该连接断开时调用）。只有在注册表中该 agent_id
// 仍指向**本连接**时才删除：新连接顶替旧连接后，旧连接的 readLoop 结束也会走到这里，
// 若无条件 delete 会把刚注册的新连接误删（竞态，见 TestReRegisterReplacesStaleConnection
// 偶发超时）。被顶替的旧连接已由 registerConn 关闭，这里不应再清理新连接。
func (r *Registry) UnregisterConn(a *agentConn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a == nil {
		return
	}
	if cur, ok := r.agents[a.info.AgentID]; ok && cur == a {
		a.closeAllStreams()
		delete(r.agents, a.info.AgentID)
	}
}

// DisconnectAgent 主动断开某 agent 的在线连接并从注册表移除（吊销 token 后由 UI 调用；
// 与 UnregisterConn 相同身份语义，但这里是服务端主动踢）。
// 该工作空间若由机器级隧道服务：立即停止路由（从该机器的发布清单剔除），并通知**服务它的**
// 那台机器的 supervisor 不再服务它（supervisor 侧记录到本次进程生命周期内生效，
// 重启后按 link.json 恢复）。
func (r *Registry) DisconnectAgent(agentID string) {
	agentID = strings.TrimSpace(agentID)
	r.mu.Lock()
	a, ok := r.agents[agentID]
	if ok {
		delete(r.agents, agentID)
	}
	machineID, _, served := r.findHubEntryLocked(agentID)
	hub := r.hubs[machineID]
	r.mu.Unlock()
	if ok && a != nil {
		a.closeFromGateway()
	}
	if !served {
		return
	}
	r.dropHubAgent(agentID)
	if hub != nil {
		_ = hub.send(tunnel.Frame{Type: tunnel.FrameRevoke, AgentID: agentID})
	}
}

// RegisterAgent 直接注册一个 agent 记录（无真实 WSS；测试注入或调试用）。
// 与 handler 正常注册路径共用 registerConn，顶替语义一致。
func (r *Registry) RegisterAgent(agentID, machineID string) error {
	return r.registerConn(&agentConn{info: AgentInfo{AgentID: agentID, MachineID: machineID}})
}

// OnlineAgents 返回当前**可路由**的工作空间列表（稳定排序）：
//   - 自持隧道的在线 agent（Sleeping=false）
//   - 机器级隧道发布的工作空间：agent 进程可能已空闲退出（Sleeping=true），
//     网关仍可路由——首次消息会触发 supervisor 按需拉起再转发
func (r *Registry) OnlineAgents() []AgentInfo {
	r.mu.Lock()
	infos := make([]AgentInfo, 0, len(r.agents))
	seen := make(map[string]bool, len(r.agents))
	for _, a := range r.agents {
		infos = append(infos, a.infoSnapshot())
		seen[a.info.AgentID] = true
	}
	for machineID, entries := range r.hubAgents {
		for _, e := range entries {
			if seen[e.AgentID] {
				continue // 直连优先（legacy agent 仍自持隧道时）
			}
			seen[e.AgentID] = true
			infos = append(infos, AgentInfo{
				AgentID:   e.AgentID,
				Name:      e.Name,
				RootPath:  e.RootPath,
				MachineID: machineID,
				Sleeping:  true,
			})
		}
	}
	r.mu.Unlock()
	sortAgentInfos(infos)
	return infos
}

// AgentAlive 某工作空间是否可路由（直连 agent 在线，或机器级隧道在服务它）。
func (r *Registry) AgentAlive(agentID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.agents[agentID]; ok {
		return true
	}
	return r.hubForAgentLocked(agentID) != nil
}

// FindAgent 按 id 查可路由的 agent 信息（含机器级隧道发布的休眠工作空间）。
func (r *Registry) FindAgent(agentID string) (AgentInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a, ok := r.agents[agentID]; ok {
		return a.infoSnapshot(), true
	}
	machineID, entry, ok := r.findHubEntryLocked(agentID)
	if !ok {
		return AgentInfo{}, false
	}
	return AgentInfo{
		AgentID:   entry.AgentID,
		Name:      entry.Name,
		RootPath:  entry.RootPath,
		MachineID: machineID,
		Sleeping:  true,
	}, true
}

// FindAgentByMachine 返回某机器（machine_id）当前的隧道连接（用于下发
// register 控制帧：register 是机器级操作，经该机器任一连接转交本机 supervisor）。
// 机器级隧道优先（常驻），其次该机器自持隧道的 agent。
// 返回 nil 表示该机器当前无在线连接（如首次接入，需机器侧手动 cata link add）。
func (r *Registry) FindAgentByMachine(machineID string) *agentConn {
	machineID = strings.TrimSpace(machineID)
	if machineID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok := r.hubs[machineID]; ok {
		return h
	}
	for _, a := range r.agents {
		if a.info.MachineID == machineID {
			return a
		}
	}
	return nil
}

// Machines 返回当前在线的机器标识列表（去重、稳定排序），供 UI 按机器分组。
func (r *Registry) Machines() []string {
	r.mu.Lock()
	seen := map[string]bool{}
	for m := range r.hubs {
		if m != "" {
			seen[m] = true
		}
	}
	for _, a := range r.agents {
		if m := strings.TrimSpace(a.info.MachineID); m != "" {
			seen[m] = true
		}
	}
	r.mu.Unlock()
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// SendRegister 向某机器下发 register 控制帧（subpath 相对该机 workspace_root）。
func (r *Registry) SendRegister(machineID, subpath string) error {
	a := r.FindAgentByMachine(machineID)
	if a == nil {
		return fmt.Errorf("machine %q has no online tunnel", machineID)
	}
	return a.send(tunnel.Frame{Type: tunnel.FrameRegister, RootPath: subpath})
}

// DialAgent 打开一条到某工作空间的逻辑连接（stream）。返回的 net.Conn
// 由调用方（socketclient.Conn）当作普通 Unix socket 连接使用——NDJSON chat 协议零改动。
//
// 路由：自持隧道的 agent 直连优先；否则用服务该工作空间的**机器级隧道**，
// 由 supervisor 侧按需拉起 agent 进程再拨其 per-ws socket（冷启动可能数秒）。
func (r *Registry) DialAgent(agentID string) (net.Conn, error) {
	r.mu.Lock()
	target, hint := r.routeTargetLocked(agentID)
	r.mu.Unlock()
	if target == nil {
		return nil, fmt.Errorf("agent %q not reachable (no online tunnel serves it)", agentID)
	}
	return target.openStream(hint)
}

// routeTargetLocked 决定某工作空间的 stream 走哪条连接（调用方持锁）：
// 自持隧道的在线 agent 直连优先，否则用服务它的机器级隧道（hint = 目标 agent，
// 让 supervisor 侧按需拉起再转发）。返回 nil 表示当前不可达。
func (r *Registry) routeTargetLocked(agentID string) (*agentConn, string) {
	if a, ok := r.agents[agentID]; ok {
		return a, ""
	}
	if hub := r.hubForAgentLocked(agentID); hub != nil {
		return hub, agentID
	}
	return nil, ""
}

// hubForAgentLocked 返回服务该工作空间的机器级隧道（调用方持锁）。
func (r *Registry) hubForAgentLocked(agentID string) *agentConn {
	for machineID, entries := range r.hubAgents {
		for _, e := range entries {
			if e.AgentID == agentID {
				return r.hubs[machineID]
			}
		}
	}
	return nil
}

// findHubEntryLocked 查某工作空间由哪台机器（经机器级隧道）发布（调用方持锁）。
func (r *Registry) findHubEntryLocked(agentID string) (string, tunnel.AgentEntry, bool) {
	for machineID, entries := range r.hubAgents {
		for _, e := range entries {
			if e.AgentID == agentID {
				return machineID, e, true
			}
		}
	}
	return "", tunnel.AgentEntry{}, false
}

func sortAgentInfos(infos []AgentInfo) {
	for i := 1; i < len(infos); i++ {
		for j := i; j > 0 && infos[j].AgentID < infos[j-1].AgentID; j-- {
			infos[j], infos[j-1] = infos[j-1], infos[j]
		}
	}
}

// agentConn 网关侧对单个 WSS 隧道连接的管理（既用于 agent 自持隧道，也用于机器级隧道）。
type agentConn struct {
	info       AgentInfo
	ws         *websocket.Conn
	writeMu    sync.Mutex
	nextStream uint64
	streamsMu  sync.Mutex
	streams    map[uint64]*streamConn
	closed     atomic.Bool

	// pingInterval 心跳周期（>0 时网关主动发 ping 并设读 deadline 检测半开连接）。
	pingInterval time.Duration

	// openTimeout stream 打开等待上限：机器级隧道要按需拉起 agent 进程（冷启动），
	// 需要比直连更长的等待（见 hubStreamOpenTimeout）。
	openTimeout time.Duration

	// onAgents 机器级隧道推送工作空间清单（agents 帧）时的回调；由 Registry 注入。
	onAgents func([]tunnel.AgentEntry)
}

func newAgentConn(ws *websocket.Conn, info AgentInfo) *agentConn {
	return &agentConn{
		info:    info,
		ws:      ws,
		streams: map[uint64]*streamConn{},
	}
}

func (a *agentConn) infoSnapshot() AgentInfo {
	return a.info
}

func (a *agentConn) send(f tunnel.Frame) error {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	if a.closed.Load() {
		return io.ErrClosedPipe
	}
	return a.ws.WriteJSON(f)
}

// hubStreamOpenTimeout 机器级隧道打开 stream 的等待上限：supervisor 收到 open 后要
// 按需拉起 agent 进程（冷启动含工作空间绑定 + socket 监听，实测数百毫秒~数秒），
// 直连 agent 则用较短上限。
const (
	hubStreamOpenTimeout    = 60 * time.Second
	directStreamOpenTimeout = 10 * time.Second
)

// openStream 打开一条新 stream。agentHint 非空 = 机器级隧道：让 supervisor 为
// 该工作空间按需拉起 agent 并把 stream 接到它的 per-ws socket。
func (a *agentConn) openStream(agentHint string) (net.Conn, error) {
	if a.closed.Load() {
		return nil, io.ErrClosedPipe
	}
	a.streamsMu.Lock()
	a.nextStream++
	id := a.nextStream
	a.streamsMu.Unlock()

	sc := &streamConn{
		agent:  a,
		id:     id,
		rch:    make(chan []byte, 64),
		errCh:  make(chan error, 1),
		opened: make(chan struct{}),
		done:   make(chan struct{}),
	}
	a.streamsMu.Lock()
	a.streams[id] = sc
	a.streamsMu.Unlock()

	if err := a.send(tunnel.Frame{Type: tunnel.FrameOpen, Stream: id, AgentID: agentHint}); err != nil {
		a.dropStream(id)
		return nil, err
	}
	timeout := a.openTimeout
	if timeout <= 0 {
		timeout = directStreamOpenTimeout
	}
	select {
	case <-sc.opened:
		return sc, nil
	case err := <-sc.errCh:
		a.dropStream(id)
		return nil, err
	case <-time.After(timeout):
		a.dropStream(id)
		return nil, fmt.Errorf("agent %s: stream %d open timeout", a.info.AgentID, id)
	}
}

func (a *agentConn) dropStream(id uint64) {
	a.streamsMu.Lock()
	delete(a.streams, id)
	a.streamsMu.Unlock()
}

func (a *agentConn) closeAllStreams() {
	a.streamsMu.Lock()
	scs := make([]*streamConn, 0, len(a.streams))
	for _, sc := range a.streams {
		scs = append(scs, sc)
	}
	a.streams = map[uint64]*streamConn{}
	a.streamsMu.Unlock()
	for _, sc := range scs {
		sc.closeFromRemote(nil)
	}
}

// handleFrame 处理来自 worker 的帧。
func (a *agentConn) handleFrame(f tunnel.Frame) {
	switch f.Type {
	case tunnel.FrameOpened:
		if sc := a.getStream(f.Stream); sc != nil {
			select {
			case <-sc.opened:
			default:
				close(sc.opened)
			}
		}
	case tunnel.FrameLine:
		sc := a.getStream(f.Stream)
		if sc == nil {
			return
		}
		data, err := tunnel.DecodeData(f.Data)
		if err != nil {
			sc.pushError(fmt.Errorf("bad line data: %w", err))
			return
		}
		// 背压：非阻塞入队 + 字节配额。慢消费者（本地 socket 读得慢）填满配额时
		// 只关闭该流，绝不阻塞 agentConn.readLoop（否则该 agent 的所有流一起停摆）。
		if sc.queuedBytes.Load() > maxStreamQueuedBytes {
			log.Printf("registry: stream %d over byte quota (%d), closing slow stream", sc.id, sc.queuedBytes.Load())
			sc.closeFromRemote(fmt.Errorf("stream over byte quota"))
			return
		}
		select {
		case sc.rch <- data:
			sc.queuedBytes.Add(int64(len(data)))
		case <-sc.done:
		default:
			// rch 满但未超字节配额（条目上限）：仍关闭慢流，避免 readLoop 阻塞。
			log.Printf("registry: stream %d channel full, closing slow stream", sc.id)
			sc.closeFromRemote(fmt.Errorf("stream channel full"))
		}
	case tunnel.FrameClose:
		if sc := a.getStream(f.Stream); sc != nil {
			sc.closeFromRemote(nil)
		}
	case tunnel.FrameError:
		if sc := a.getStream(f.Stream); sc != nil {
			sc.pushError(fmt.Errorf("%s", f.Message))
		}
	case tunnel.FramePing:
		_ = a.send(tunnel.Frame{Type: tunnel.FramePong})
	case tunnel.FramePong:
		// ignore
	case tunnel.FrameAgents:
		// 机器级隧道推送最新工作空间清单（link.json 变化）。
		if a.onAgents != nil {
			a.onAgents(f.Agents)
		}
	}
}

func (a *agentConn) getStream(id uint64) *streamConn {
	a.streamsMu.Lock()
	defer a.streamsMu.Unlock()
	return a.streams[id]
}

// readLoop 消费 worker 帧直到断开。设读 deadline 检测半开连接：
// 网关周期 ping，若超时未收到任何帧（pong/数据）即判定连接死亡并关闭。
func (a *agentConn) readLoop() {
	defer a.ws.Close()
	if a.pingInterval > 0 {
		_ = a.ws.SetReadDeadline(time.Now().Add(3 * a.pingInterval))
	}
	for {
		_ = a.ws.SetReadDeadline(time.Now().Add(3 * maxInt64(a.pingInterval, 10*time.Second)))
		_, msg, err := a.ws.ReadMessage()
		if err != nil {
			return
		}
		// 收到任何帧都说明连接活着，刷新 deadline。
		if a.pingInterval > 0 {
			_ = a.ws.SetReadDeadline(time.Now().Add(3 * a.pingInterval))
		}
		var f tunnel.Frame
		if err := json.Unmarshal(msg, &f); err != nil {
			continue
		}
		a.handleFrame(f)
	}
}

// startHeartbeat 周期向 worker 发送 ping，配合 readLoop 的读 deadline 检测半开连接。
// 只在网关侧启用（worker 侧重连退避由 RunTunnelWorker 负责）。
func (a *agentConn) startHeartbeat(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	a.pingInterval = interval
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if a.closed.Load() {
					return
				}
				if err := a.send(tunnel.Frame{Type: tunnel.FramePing}); err != nil {
					return
				}
			}
		}
	}()
}

// closeFromGateway 关闭本连接及其所有流（新 hello 顶替旧连接时调用）。
func (a *agentConn) closeFromGateway() {
	if a.closed.CompareAndSwap(false, true) {
		a.closeAllStreams()
		if a.ws != nil {
			_ = a.ws.Close()
		}
	}
}

func maxInt64(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

// maxStreamQueuedBytes 单流未消费字节配额上限（16 MiB）。慢消费者超过即断流重连，
// 防止一条慢流把整个 agent 连接的多路复用拖死（旧实现 rch 满后阻塞 readLoop）。
const maxStreamQueuedBytes = 16 << 20

// streamConn net.Conn 适配器：把隧道 stream 变成可读写的字节流。
type streamConn struct {
	agent  *agentConn
	id     uint64
	rch    chan []byte
	errCh  chan error
	opened chan struct{}

	rmu    sync.Mutex
	rbuf   bytes.Buffer
	closed atomic.Bool
	done   chan struct{}
	once   sync.Once

	// 背压：每流有界字节配额。FrameLine 入队非阻塞（不拖死 agentConn.readLoop），
	// 队列字节数超限时关闭该流（慢消费者断流重连），而不是阻塞整个多路复用器。
	queuedBytes atomic.Int64
}

func (c *streamConn) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	for c.rbuf.Len() == 0 {
		select {
		case chunk := <-c.rch:
			c.rbuf.Write(chunk)
			c.queuedBytes.Add(-int64(len(chunk)))
		case err := <-c.errCh:
			return 0, err
		case <-c.done:
			return 0, io.EOF
		}
	}
	return c.rbuf.Read(p)
}

func (c *streamConn) Write(p []byte) (int, error) {
	if c.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	if len(p) > tunnel.MaxFrameBytes {
		return 0, fmt.Errorf("tunnel: write exceeds %d bytes", tunnel.MaxFrameBytes)
	}
	if err := c.agent.send(tunnel.Frame{Type: tunnel.FrameLine, Stream: c.id, Data: tunnel.EncodeData(p)}); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *streamConn) Close() error {
	c.once.Do(func() {
		if c.closed.CompareAndSwap(false, true) {
			_ = c.agent.send(tunnel.Frame{Type: tunnel.FrameClose, Stream: c.id})
			close(c.done)
			c.agent.dropStream(c.id)
		}
	})
	return nil
}

func (c *streamConn) closeFromRemote(err error) {
	c.once.Do(func() {
		if c.closed.CompareAndSwap(false, true) {
			if err != nil {
				c.pushError(err)
			}
			close(c.done)
			c.agent.dropStream(c.id)
		}
	})
}

func (c *streamConn) pushError(err error) {
	select {
	case c.errCh <- err:
	default:
	}
}

func (c *streamConn) LocalAddr() net.Addr                { return c.agent.ws.LocalAddr() }
func (c *streamConn) RemoteAddr() net.Addr               { return c.agent.ws.RemoteAddr() }
func (c *streamConn) SetDeadline(t time.Time) error      { return nil }
func (c *streamConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *streamConn) SetWriteDeadline(t time.Time) error { return nil }
