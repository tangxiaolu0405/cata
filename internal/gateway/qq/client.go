package qq

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	apiBaseProduction = "https://api.sgroup.qq.com"
	apiBaseSandbox    = "https://sandbox.api.sgroup.qq.com"
	messageMaxLen     = 2000
	msgSeqTTL         = 5 * time.Minute
)

// Client QQ OpenAPI（REST）。
type Client struct {
	tokens  *TokenSource
	sandbox bool
	http    *http.Client

	msgSeqMu  sync.Mutex
	msgSeqMap map[string]*msgSeqEntry
}

type msgSeqEntry struct {
	seq       atomic.Int32
	createdAt time.Time
}

// NewClient 创建 OpenAPI 客户端。
func NewClient(tokens *TokenSource, sandbox bool) *Client {
	return &Client{
		tokens:    tokens,
		sandbox:   sandbox,
		http:      &http.Client{Timeout: 30 * time.Second},
		msgSeqMap: make(map[string]*msgSeqEntry),
	}
}

func (c *Client) apiBase() string {
	if c.sandbox {
		return apiBaseSandbox
	}
	return apiBaseProduction
}

// GatewayURL 获取 WebSocket 接入地址。
func (c *Client) GatewayURL(ctx context.Context) (string, error) {
	var result struct {
		URL string `json:"url"`
	}
	if err := c.doJSON(ctx, http.MethodGet, c.apiBase()+"/gateway/bot", nil, &result); err != nil {
		return "", err
	}
	if strings.TrimSpace(result.URL) == "" {
		return "", fmt.Errorf("empty gateway url")
	}
	return result.URL, nil
}

// ReplyTarget 被动回复目标。
type ReplyTarget struct {
	Kind       string // c2c | group
	UserID     string
	GroupID    string
	EventMsgID string
}

// SendText 发送文本被动回复（带 msg_id / msg_seq）。
func (c *Client) SendText(ctx context.Context, target ReplyTarget, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		content = "(empty)"
	}
	for _, part := range SplitMessage(content, messageMaxLen) {
		if err := c.sendOne(ctx, target, part); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) sendOne(ctx context.Context, target ReplyTarget, content string) error {
	var url string
	switch target.Kind {
	case "group":
		url = fmt.Sprintf("%s/v2/groups/%s/messages", c.apiBase(), target.GroupID)
	case "c2c":
		url = fmt.Sprintf("%s/v2/users/%s/messages", c.apiBase(), target.UserID)
	default:
		return fmt.Errorf("unknown reply kind %q", target.Kind)
	}
	body := map[string]any{
		"content":  content,
		"msg_type": 0,
	}
	if target.EventMsgID != "" {
		body["msg_id"] = target.EventMsgID
		body["msg_seq"] = c.nextMsgSeq(target.EventMsgID)
	}
	return c.doJSON(ctx, http.MethodPost, url, body, nil)
}

func (c *Client) nextMsgSeq(eventMsgID string) int32 {
	c.msgSeqMu.Lock()
	defer c.msgSeqMu.Unlock()
	now := time.Now()
	for k, v := range c.msgSeqMap {
		if now.Sub(v.createdAt) > msgSeqTTL {
			delete(c.msgSeqMap, k)
		}
	}
	entry, ok := c.msgSeqMap[eventMsgID]
	if !ok {
		entry = &msgSeqEntry{createdAt: now}
		c.msgSeqMap[eventMsgID] = entry
	}
	return entry.seq.Add(1)
}

func (c *Client) doJSON(ctx context.Context, method, url string, body any, out any) error {
	if err := c.doJSONOnce(ctx, method, url, body, out, false); err != nil {
		if strings.Contains(err.Error(), "HTTP 401") {
			return c.doJSONOnce(ctx, method, url, body, out, true)
		}
		return err
	}
	return nil
}

func (c *Client) doJSONOnce(ctx context.Context, method, url string, body any, out any, forceRefresh bool) error {
	if forceRefresh {
		_ = c.tokens.Refresh(ctx)
	}
	auth, err := c.tokens.AuthorizationHeader(ctx)
	if err != nil {
		return err
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", auth)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s HTTP %d: %s", method, url, resp.StatusCode, truncate(string(raw), 300))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// SplitMessage 按 QQ 文本长度限制切分。
func SplitMessage(text string, max int) []string {
	if max <= 0 {
		max = messageMaxLen
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{"(empty)"}
	}
	if len(text) <= max {
		return []string{text}
	}
	var parts []string
	for len(text) > max {
		cut := max
		if i := strings.LastIndex(text[:max], "\n"); i > max/2 {
			cut = i
		}
		parts = append(parts, strings.TrimSpace(text[:cut]))
		text = strings.TrimSpace(text[cut:])
	}
	if text != "" {
		parts = append(parts, text)
	}
	return parts
}

// StripAtMention 去掉群聊内容前缀 <@!botid>。
func StripAtMention(content string) string {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "<@!") {
		if idx := strings.Index(content, ">"); idx >= 0 {
			content = strings.TrimSpace(content[idx+1:])
		}
	}
	return content
}

// SessionIDFor 构造 worker 会话键 id 段。
func SessionIDFor(msg IncomingMessage) string {
	switch msg.Kind {
	case "group":
		return "group_" + msg.GroupOpenID
	default:
		return "c2c_" + msg.UserOpenID
	}
}
