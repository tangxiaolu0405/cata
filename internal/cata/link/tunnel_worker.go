package link

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"cata/internal/cata/config"
	"cata/internal/cata/tunnel"
)

// errAgentTokenReady 哨兵：hello_ack 已收（token 落盘）或网关要求带 agent_token 重连——
// 触发外层立即用新 cfg 重连，而非按退避等待。
var errAgentTokenReady = errors.New("agent_token ready, reconnect")

// RunTunnelWorker agent 进程持有到网关的 WSS 隧道（cata agent --link）。
// 断线自动重连，退避 1s → 30s。ctx 取消（agent 退出）时返回 nil。
func RunTunnelWorker(ctx context.Context, agentID string) error {
	backoff := time.Second
	for {
		// 每次重载 cfg：首次注册的 agent_token 经 hello_ack 落盘后，重连即携带。
		cfg, err := LoadConfig()
		if err != nil {
			return err
		}
		if !cfg.GatewayConfigured() {
			return fmt.Errorf("link.json: gateway_url/token not configured")
		}
		if !cfg.HasAgent(agentID) {
			return fmt.Errorf("link.json: agent %q not registered", agentID)
		}
		err = runOneTunnel(ctx, agentID, cfg)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			if errors.Is(err, errAgentTokenReady) {
				backoff = time.Second // token 就绪/刷新：立即重连，不按失败退避
				log.Printf("cata agent %s: tunnel: %v", agentID, err)
				continue
			}
			log.Printf("cata agent %s: tunnel: %v (reconnect in %s)", agentID, err, backoff)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

func tunnelWSURL(gatewayURL, agentID string) (string, error) {
	gw := strings.TrimSpace(gatewayURL)
	if gw == "" {
		return "", fmt.Errorf("empty gateway url")
	}
	u, err := url.Parse(normalizeGatewayURL(gw))
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported gateway url scheme %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/cata/v1/tunnel"
	q := u.Query()
	q.Set("agent", agentID)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func runOneTunnel(ctx context.Context, agentID string, cfg Config) error {
	entry := cfg.Agents[agentID]
	wsURL, err := tunnelWSURL(cfg.GatewayURL, agentID)
	if err != nil {
		return err
	}
	if strings.HasPrefix(wsURL, "ws://") {
		log.Printf("cata agent %s: WARNING: gateway url is ws:// (token sent in plaintext); use wss:// in production", agentID)
	}
	header := http.Header{}
	// 隧道握手鉴权用逐机器 token（machine_token），不再使用固定 gateway_token。
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

	hello := tunnel.Frame{
		Type:         tunnel.FrameHello,
		AgentID:      agentID,
		Name:         entry.Name,
		RootPath:     entry.RootPath,
		MachineID:    cfg.MachineID,
		MachineToken: cfg.MachineToken,
		AgentToken:   cfg.AgentTokenFor(agentID), // per-agent token；空 = 首次注册回退 machine
		Protocol:     tunnel.ProtocolName,
		Version:      tunnel.Version,
	}
	if err := conn.WriteJSON(hello); err != nil {
		return err
	}
	log.Printf("cata agent %s: tunnel connected: %s", agentID, wsURL)

	ws := &tunnelConn{
		conn:    conn,
		streams: map[uint64]*tunnelStream{},
		errCh:   make(chan error, 1),
	}
	defer ws.closeAll()

	conn.SetReadLimit(int64(tunnel.MaxFrameBytes))
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-ws.errCh:
			return err
		default:
		}
		// 网关侧周期 ping（HeartbeatInterval）；读 deadline 设为 3×，
		// 网关静默消失（NAT 超时等）时 worker 能自行感知并重连。
		_ = conn.SetReadDeadline(time.Now().Add(3 * tunnel.HeartbeatInterval))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var f tunnel.Frame
		if err := json.Unmarshal(msg, &f); err != nil {
			log.Printf("cata agent %s: tunnel: bad frame: %v", agentID, err)
			continue
		}
		// 首次注册：网关经 hello_ack 下发 per-agent token。落盘后立即重连，
		// 用带 AgentToken 的 hello 建立正式隧道（一次性握手通道不承载业务流）。
		if f.Type == tunnel.FrameHelloAck {
			if tok := strings.TrimSpace(f.AgentToken); tok != "" {
				if err := SetAgentTokenFor(agentID, tok); err != nil {
					log.Printf("cata agent %s: persist agent_token: %v", agentID, err)
				} else {
					log.Printf("cata agent %s: got per-agent token, reconnecting with agent_token", agentID)
				}
			}
			return errAgentTokenReady
		}
		// 网关要求用 per-agent token（机器合法但 token 缺失/已吊销）：同样重连。
		if f.Type == tunnel.FrameError && strings.Contains(f.Message, "agent_token") {
			return errAgentTokenReady
		}
		ws.handleFrame(agentID, f)
	}
}

// tunnelConn worker 侧 stream 管理：每条 stream = 一条到本地 per-ws socket 的连接。
type tunnelConn struct {
	mu      sync.Mutex
	conn    *websocket.Conn
	streams map[uint64]*tunnelStream
	errCh   chan error // 连接级错误（FrameError）→ 通知 readLoop 返回触发重连
}

// notifyError 非阻塞投递连接级错误（网关 FrameError）。
func (t *tunnelConn) notifyError(err error) {
	select {
	case t.errCh <- err:
	default:
	}
}

func (t *tunnelConn) send(f tunnel.Frame) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	// 写 deadline：网关端慢读导致 WebSocket 写缓冲满时，WriteJSON 会阻塞持锁，
	// 拖住该 agent 的所有流。加短超时，超时即断开（连接级，触发重连）。
	_ = t.conn.SetWriteDeadline(time.Now().Add(tunnelSendTimeout))
	err := t.conn.WriteJSON(f)
	_ = t.conn.SetWriteDeadline(time.Time{})
	return err
}

// tunnelSendTimeout 隧道帧写出超时（WebSocket 写缓冲满时的保护）。
const tunnelSendTimeout = 15 * time.Second

func (t *tunnelConn) handleFrame(agentID string, f tunnel.Frame) {
	switch f.Type {
	case tunnel.FrameOpen:
		t.openStream(agentID, f.Stream)
	case tunnel.FrameLine:
		if s := t.getStream(f.Stream); s != nil {
			data, err := tunnel.DecodeData(f.Data)
			if err != nil {
				log.Printf("cata agent %s: stream %d: bad data: %v", agentID, f.Stream, err)
				return
			}
			if _, err := s.write(data); err != nil {
				_ = t.send(tunnel.Frame{Type: tunnel.FrameClose, Stream: f.Stream})
				s.close()
				t.removeStream(f.Stream)
			}
		}
	case tunnel.FrameClose:
		if s := t.removeStream(f.Stream); s != nil {
			s.close()
		}
	case tunnel.FrameError:
		// 连接级错误（如重复注册被顶替、协议不匹配）：记录并触发重连，
		// 避免无限静默重连看不到根因。
		log.Printf("cata agent %s: tunnel error from gateway: %s", agentID, f.Message)
		t.notifyError(fmt.Errorf("gateway error: %s", f.Message))
	case tunnel.FrameRegister:
		// 网关下发的「注册新工作空间」控制帧：校验子路径在 workspace_root 下，
		// 经 supervisor.sock 转交 supervisor 执行 Add + EnsureAgent。
		// 新 agent 进程自带 --link 回连网关，网关 registry 即出现新 agent。
		if err := HandleRemoteRegister(f.RootPath); err != nil {
			log.Printf("cata agent %s: remote register %q: %v", agentID, f.RootPath, err)
			_ = t.send(tunnel.Frame{Type: tunnel.FrameError, Message: fmt.Sprintf("register: %v", err)})
		} else {
			log.Printf("cata agent %s: remote register %q accepted", agentID, f.RootPath)
		}
	case tunnel.FramePing:
		_ = t.send(tunnel.Frame{Type: tunnel.FramePong})
	case tunnel.FramePong:
		// ignore
	}
}

func (t *tunnelConn) openStream(agentID string, id uint64) {
	socketPath := config.ResolvedAgentSocketPath(agentID)
	sock, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		_ = t.send(tunnel.Frame{Type: tunnel.FrameError, Stream: id, Message: fmt.Sprintf("dial %s: %v", socketPath, err)})
		_ = t.send(tunnel.Frame{Type: tunnel.FrameClose, Stream: id})
		return
	}
	s := &tunnelStream{
		id:   id,
		conn: t,
		sock: sock,
		done: make(chan struct{}),
	}
	t.mu.Lock()
	t.streams[id] = s
	t.mu.Unlock()
	_ = t.send(tunnel.Frame{Type: tunnel.FrameOpened, Stream: id})

	go s.readLoop()
}

func (t *tunnelConn) getStream(id uint64) *tunnelStream {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.streams[id]
}

func (t *tunnelConn) removeStream(id uint64) *tunnelStream {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.streams[id]
	delete(t.streams, id)
	return s
}

func (t *tunnelConn) closeAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, s := range t.streams {
		s.close()
	}
	t.streams = map[uint64]*tunnelStream{}
}

// tunnelStream 一条到本地 per-ws socket 的逻辑连接。
type tunnelStream struct {
	id   uint64
	conn *tunnelConn
	sock net.Conn
	done chan struct{}
	once sync.Once
}

func (s *tunnelStream) write(b []byte) (int, error) {
	return s.sock.Write(b)
}

func (s *tunnelStream) readLoop() {
	defer s.close()
	br := bufio.NewReaderSize(s.sock, 64*1024)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			_ = s.conn.send(tunnel.Frame{Type: tunnel.FrameClose, Stream: s.id})
			return
		}
		if len(line) > tunnel.MaxFrameBytes {
			_ = s.conn.send(tunnel.Frame{Type: tunnel.FrameError, Stream: s.id, Message: "line too large"})
			_ = s.conn.send(tunnel.Frame{Type: tunnel.FrameClose, Stream: s.id})
			return
		}
		if err := s.conn.send(tunnel.Frame{Type: tunnel.FrameLine, Stream: s.id, Data: tunnel.EncodeData(line)}); err != nil {
			return
		}
	}
}

func (s *tunnelStream) close() {
	s.once.Do(func() {
		close(s.done)
		_ = s.sock.Close()
	})
}
