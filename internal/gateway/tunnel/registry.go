// Package tunnel 网关侧 cata-tunnel.v1 服务端：接受各机器 agent 进程的 WSS 注册，
// 把 Web UI / 渠道会话变成多条 stream 反向拨到对应 agent 的本地 per-ws socket。
package tunnel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
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

// registerConn 注册一个 agent 的 WSS 连接。agent_id 已在线时拒绝（第二个 hello 冲突）。
func (r *Registry) registerConn(a *agentConn) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.agents[a.info.AgentID]; ok {
		return fmt.Errorf("agent %q already connected", a.info.AgentID)
	}
	r.agents[a.info.AgentID] = a
	return nil
}

// Unregister 移除 agent（断开时调用）。
func (r *Registry) Unregister(agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a, ok := r.agents[agentID]; ok {
		a.closeAllStreams()
	}
	delete(r.agents, agentID)
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
		select {
		case sc.rch <- data:
		case <-sc.done:
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

// readLoop 消费 worker 帧直到断开。
func (a *agentConn) readLoop() {
	defer a.ws.Close()
	for {
		_, msg, err := a.ws.ReadMessage()
		if err != nil {
			return
		}
		var f tunnel.Frame
		if err := json.Unmarshal(msg, &f); err != nil {
			continue
		}
		a.handleFrame(f)
	}
}

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
}

func (c *streamConn) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	for c.rbuf.Len() == 0 {
		select {
		case chunk := <-c.rch:
			c.rbuf.Write(chunk)
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
