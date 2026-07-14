package qq

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	intentGroupAndC2C = 1 << 25

	opDispatch       = 0
	opHeartbeat      = 1
	opIdentify       = 2
	opResume         = 6
	opReconnect      = 7
	opInvalidSession = 9
	opHello          = 10
	opHeartbeatACK   = 11

	maxReconnectBackoff  = 60 * time.Second
	maxReconnectAttempts = 30
)

type wsPayload struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d,omitempty"`
	S  *int64          `json:"s,omitempty"`
	T  string          `json:"t,omitempty"`
}

// IncomingMessage 解析后的入站消息。
type IncomingMessage struct {
	Kind        string // c2c | group
	MsgID       string
	UserOpenID  string
	GroupOpenID string
	Content     string
}

// MessageHandler 处理入站消息。
type MessageHandler func(ctx context.Context, msg IncomingMessage)

// Gateway WebSocket 会话（官方已声明逐步下线，仅作试验接入）。
type Gateway struct {
	client  *Client
	tokens  *TokenSource
	intents int
	handler MessageHandler

	wsMu         sync.Mutex
	wsConn       *websocket.Conn
	sessionID    string
	lastSeq      atomic.Int64
	heartbeatMs  int
	heartbeatOK  atomic.Bool
	reconnecting atomic.Bool
}

// NewGateway 创建网关。
func NewGateway(client *Client, tokens *TokenSource, handler MessageHandler) *Gateway {
	return &Gateway{
		client:  client,
		tokens:  tokens,
		intents: intentGroupAndC2C,
		handler: handler,
	}
}

// Run 连接并保持直到 ctx 取消。
func (g *Gateway) Run(ctx context.Context) error {
	if err := g.connect(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	g.closeConn()
	return ctx.Err()
}

func (g *Gateway) connect(ctx context.Context) error {
	token, err := g.tokens.AccessToken(ctx)
	if err != nil {
		return err
	}
	url, err := g.client.GatewayURL(ctx)
	if err != nil {
		return fmt.Errorf("gateway url: %w", err)
	}
	log.Printf("qq: dialing websocket %s", url)
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("ws dial: %w (official WebSocket may be disabled for this bot; see botgo webhook)", err)
	}
	g.wsMu.Lock()
	g.wsConn = conn
	g.wsMu.Unlock()

	if err := g.waitHello(conn); err != nil {
		conn.Close()
		return err
	}
	if err := g.sendIdentify(conn, token); err != nil {
		conn.Close()
		return err
	}
	if err := g.waitReady(conn); err != nil {
		conn.Close()
		return err
	}
	go g.heartbeatLoop(ctx)
	go g.readLoop(ctx)
	return nil
}

func (g *Gateway) waitHello(conn *websocket.Conn) error {
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	var msg wsPayload
	if err := conn.ReadJSON(&msg); err != nil {
		return fmt.Errorf("hello: %w", err)
	}
	if msg.Op != opHello {
		return fmt.Errorf("expected Hello(10), got op=%d", msg.Op)
	}
	var hello struct {
		HeartbeatInterval int `json:"heartbeat_interval"`
	}
	_ = json.Unmarshal(msg.D, &hello)
	if hello.HeartbeatInterval > 0 {
		g.heartbeatMs = hello.HeartbeatInterval
	} else {
		g.heartbeatMs = 41250
	}
	g.heartbeatOK.Store(true)
	return nil
}

func (g *Gateway) sendIdentify(conn *websocket.Conn, token string) error {
	payload := map[string]any{
		"op": opIdentify,
		"d": map[string]any{
			"token":   "QQBot " + token,
			"intents": g.intents,
			"shard":   [2]int{0, 1},
		},
	}
	g.wsMu.Lock()
	defer g.wsMu.Unlock()
	return conn.WriteJSON(payload)
}

func (g *Gateway) waitReady(conn *websocket.Conn) error {
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	for {
		var msg wsPayload
		if err := conn.ReadJSON(&msg); err != nil {
			return fmt.Errorf("ready: %w", err)
		}
		if msg.S != nil {
			g.lastSeq.Store(*msg.S)
		}
		if msg.Op == opHeartbeatACK {
			g.heartbeatOK.Store(true)
			continue
		}
		if msg.Op == opDispatch && msg.T == "READY" {
			var ready struct {
				SessionID string `json:"session_id"`
			}
			_ = json.Unmarshal(msg.D, &ready)
			g.sessionID = ready.SessionID
			log.Printf("qq: gateway READY session=%s", g.sessionID)
			return nil
		}
		if msg.Op == opInvalidSession {
			return fmt.Errorf("invalid session on identify (WebSocket may be unavailable for this bot)")
		}
		return fmt.Errorf("expected READY, got op=%d t=%s", msg.Op, msg.T)
	}
}

func (g *Gateway) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(g.heartbeatMs) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !g.heartbeatOK.Load() {
				log.Printf("qq: heartbeat ack missing, reconnecting")
				g.triggerReconnect(ctx)
				return
			}
			g.heartbeatOK.Store(false)
			g.sendHeartbeat()
		}
	}
}

func (g *Gateway) sendHeartbeat() {
	seq := g.lastSeq.Load()
	var d json.RawMessage
	if seq > 0 {
		d, _ = json.Marshal(seq)
	} else {
		d = json.RawMessage("null")
	}
	g.wsMu.Lock()
	defer g.wsMu.Unlock()
	if g.wsConn != nil {
		_ = g.wsConn.WriteJSON(wsPayload{Op: opHeartbeat, D: d})
	}
}

func (g *Gateway) readLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		g.wsMu.Lock()
		conn := g.wsConn
		g.wsMu.Unlock()
		if conn == nil {
			return
		}
		var msg wsPayload
		if err := conn.ReadJSON(&msg); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("qq: ws read error: %v", err)
			g.triggerReconnect(ctx)
			return
		}
		if msg.S != nil {
			g.lastSeq.Store(*msg.S)
		}
		switch msg.Op {
		case opDispatch:
			g.handleDispatch(ctx, msg.T, msg.D)
		case opHeartbeat:
			g.sendHeartbeat()
		case opReconnect:
			log.Printf("qq: server requested reconnect")
			g.triggerReconnect(ctx)
			return
		case opInvalidSession:
			var resumable bool
			_ = json.Unmarshal(msg.D, &resumable)
			if !resumable {
				g.sessionID = ""
			}
			log.Printf("qq: invalid session resumable=%v", resumable)
			g.triggerReconnect(ctx)
			return
		case opHeartbeatACK:
			g.heartbeatOK.Store(true)
		}
	}
}

func (g *Gateway) handleDispatch(ctx context.Context, t string, data json.RawMessage) {
	switch t {
	case "C2C_MESSAGE_CREATE":
		msg, ok := ParseC2C(data)
		if ok && g.handler != nil {
			go g.handler(ctx, msg)
		}
	case "GROUP_AT_MESSAGE_CREATE":
		msg, ok := ParseGroupAT(data)
		if ok && g.handler != nil {
			go g.handler(ctx, msg)
		}
	case "RESUMED":
		log.Printf("qq: session resumed")
	case "READY":
	default:
		log.Printf("qq: event %s", t)
	}
}

// ParseC2C 解析单聊事件（测试可见）。
func ParseC2C(data json.RawMessage) (IncomingMessage, bool) {
	var d struct {
		ID      string `json:"id"`
		Content string `json:"content"`
		Author  struct {
			UserOpenID string `json:"user_openid"`
		} `json:"author"`
	}
	if err := json.Unmarshal(data, &d); err != nil || d.ID == "" || d.Author.UserOpenID == "" {
		return IncomingMessage{}, false
	}
	content := strings.TrimSpace(d.Content)
	if content == "" {
		return IncomingMessage{}, false
	}
	return IncomingMessage{
		Kind:       "c2c",
		MsgID:      d.ID,
		UserOpenID: d.Author.UserOpenID,
		Content:    content,
	}, true
}

// ParseGroupAT 解析群 @ 事件。
func ParseGroupAT(data json.RawMessage) (IncomingMessage, bool) {
	var d struct {
		ID          string `json:"id"`
		GroupOpenID string `json:"group_openid"`
		Content     string `json:"content"`
		Author      struct {
			MemberOpenID string `json:"member_openid"`
		} `json:"author"`
	}
	if err := json.Unmarshal(data, &d); err != nil || d.ID == "" || d.GroupOpenID == "" {
		return IncomingMessage{}, false
	}
	content := StripAtMention(d.Content)
	if content == "" {
		return IncomingMessage{}, false
	}
	return IncomingMessage{
		Kind:        "group",
		MsgID:       d.ID,
		UserOpenID:  d.Author.MemberOpenID,
		GroupOpenID: d.GroupOpenID,
		Content:     content,
	}, true
}

func (g *Gateway) triggerReconnect(ctx context.Context) {
	if !g.reconnecting.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer g.reconnecting.Store(false)
		g.reconnectLoop(ctx)
	}()
}

func (g *Gateway) reconnectLoop(ctx context.Context) {
	g.closeConn()
	backoff := time.Second
	for attempt := 1; attempt <= maxReconnectAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if err := g.tokens.Refresh(ctx); err != nil {
			log.Printf("qq: reconnect token: %v (attempt %d)", err, attempt)
			backoff = minDuration(backoff*2, maxReconnectBackoff)
			continue
		}
		token, err := g.tokens.AccessToken(ctx)
		if err != nil {
			backoff = minDuration(backoff*2, maxReconnectBackoff)
			continue
		}
		url, err := g.client.GatewayURL(ctx)
		if err != nil {
			log.Printf("qq: reconnect gateway url: %v", err)
			backoff = minDuration(backoff*2, maxReconnectBackoff)
			continue
		}
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
		if err != nil {
			log.Printf("qq: reconnect dial: %v", err)
			backoff = minDuration(backoff*2, maxReconnectBackoff)
			continue
		}
		g.wsMu.Lock()
		g.wsConn = conn
		g.wsMu.Unlock()
		if err := g.waitHello(conn); err != nil {
			conn.Close()
			backoff = minDuration(backoff*2, maxReconnectBackoff)
			continue
		}
		ok := false
		if g.sessionID != "" {
			if err := g.sendResume(conn, token); err == nil {
				ok = true
				log.Printf("qq: resumed session=%s", g.sessionID)
			} else {
				g.sessionID = ""
			}
		}
		if !ok {
			if err := g.sendIdentify(conn, token); err != nil {
				conn.Close()
				backoff = minDuration(backoff*2, maxReconnectBackoff)
				continue
			}
			if err := g.waitReady(conn); err != nil {
				conn.Close()
				backoff = minDuration(backoff*2, maxReconnectBackoff)
				continue
			}
		}
		log.Printf("qq: reconnected")
		go g.heartbeatLoop(ctx)
		go g.readLoop(ctx)
		return
	}
	log.Printf("qq: reconnect gave up after %d attempts", maxReconnectAttempts)
}

func (g *Gateway) sendResume(conn *websocket.Conn, token string) error {
	payload := map[string]any{
		"op": opResume,
		"d": map[string]any{
			"token":      "QQBot " + token,
			"session_id": g.sessionID,
			"seq":        g.lastSeq.Load(),
		},
	}
	g.wsMu.Lock()
	defer g.wsMu.Unlock()
	return conn.WriteJSON(payload)
}

func (g *Gateway) closeConn() {
	g.wsMu.Lock()
	defer g.wsMu.Unlock()
	if g.wsConn != nil {
		_ = g.wsConn.Close()
		g.wsConn = nil
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
