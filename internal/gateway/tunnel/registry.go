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
}

// Registry 在线 agent 注册中心（无状态：网关重启后 agent 重连即恢复）。
type Registry struct {
	mu     sync.Mutex
	agents map[string]*agentConn
}

// NewRegistry 创建注册中心。
func NewRegistry() *Registry {
	return &Registry{agents: map[string]*agentConn{}}
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
func (r *Registry) DisconnectAgent(agentID string) {
	r.mu.Lock()
	a, ok := r.agents[agentID]
	if ok {
		delete(r.agents, agentID)
	}
	r.mu.Unlock()
	if ok && a != nil {
		a.closeFromGateway()
	}
}

// RegisterAgent 直接注册一个 agent 记录（无真实 WSS；测试注入或调试用）。
// 与 handler 正常注册路径共用 registerConn，顶替语义一致。
func (r *Registry) RegisterAgent(agentID, machineID string) error {
	return r.registerConn(&agentConn{info: AgentInfo{AgentID: agentID, MachineID: machineID}})
}

// OnlineAgents 返回当前在线 agent 列表（稳定排序）。
func (r *Registry) OnlineAgents() []AgentInfo {
	r.mu.Lock()
	acs := make([]*agentConn, 0, len(r.agents))
	for _, a := range r.agents {
		acs = append(acs, a)
	}
	r.mu.Unlock()
	infos := make([]AgentInfo, 0, len(acs))
	for _, a := range acs {
		infos = append(infos, a.infoSnapshot())
	}
	sortAgentInfos(infos)
	return infos
}

// AgentAlive 某 agent 是否在线。
func (r *Registry) AgentAlive(agentID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.agents[agentID]
	return ok
}

// FindAgent 按 id 查在线 agent 信息。
func (r *Registry) FindAgent(agentID string) (AgentInfo, bool) {
	r.mu.Lock()
	a, ok := r.agents[agentID]
	r.mu.Unlock()
	if !ok {
		return AgentInfo{}, false
	}
	return a.infoSnapshot(), true
}

// FindAgentByMachine 返回某机器（machine_id）当前在线的任意一个 agent 连接（用于下发
// register 控制帧：register 是机器级操作，经该机器任一在线 agent 转交本机 supervisor）。
// 返回 nil 表示该机器当前无在线 agent（如首次接入，需机器侧手动 cata link add）。
func (r *Registry) FindAgentByMachine(machineID string) *agentConn {
	machineID = strings.TrimSpace(machineID)
	if machineID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
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
		return fmt.Errorf("machine %q has no online agent", machineID)
	}
	return a.send(tunnel.Frame{Type: tunnel.FrameRegister, RootPath: subpath})
}

// DialAgent 打开一条到某在线 agent 的逻辑连接（stream）。返回的 net.Conn
// 由调用方（socketclient.Conn）当作普通 Unix socket 连接使用——NDJSON chat 协议零改动。
func (r *Registry) DialAgent(agentID string) (net.Conn, error) {
	r.mu.Lock()
	a, ok := r.agents[agentID]
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("agent %q not online", agentID)
	}
	return a.openStream()
}

func sortAgentInfos(infos []AgentInfo) {
	for i := 1; i < len(infos); i++ {
		for j := i; j > 0 && infos[j].AgentID < infos[j-1].AgentID; j-- {
			infos[j], infos[j-1] = infos[j-1], infos[j]
		}
	}
}

// agentConn 网关侧对单个 agent WSS 连接的管理。
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

func (a *agentConn) openStream() (net.Conn, error) {
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

	if err := a.send(tunnel.Frame{Type: tunnel.FrameOpen, Stream: id}); err != nil {
		a.dropStream(id)
		return nil, err
	}
	select {
	case <-sc.opened:
		return sc, nil
	case err := <-sc.errCh:
		a.dropStream(id)
		return nil, err
	case <-time.After(10 * time.Second):
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
