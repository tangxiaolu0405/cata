package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"cata/internal/cata/brain"
)

type cataRequest struct {
	Command   string            `json:"command"`
	Text      string            `json:"text,omitempty"`
	Stream    bool              `json:"stream,omitempty"`
	ConfirmID string            `json:"confirm_id,omitempty"`
	Approved  bool              `json:"approved,omitempty"`
	ChoiceID  string            `json:"choice_id,omitempty"`
	Selected  []string          `json:"selected,omitempty"`
	Cwd       string            `json:"cwd,omitempty"`
	Runtime   *brain.RuntimeEnv `json:"runtime,omitempty"`
}

type cataResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ExecConfirmPrompt cata 请求用户确认执行命令。
type ExecConfirmPrompt struct {
	ConfirmID   string
	CommandLine string
	Cwd         string
}

// UserChoicePrompt cata 请求用户选择。
type UserChoicePrompt struct {
	ChoiceID string
	Prompt   string
	Options  []ChoiceOption
	Multi    bool
}

// ChoiceOption 可选项。
type ChoiceOption struct {
	ID    string
	Label string
	Desc  string
}

// StreamHandler 处理 cata 流式事件中的交互。
type StreamHandler interface {
	OnProgress(message string)
	OnToolStart(name string)
	ConfirmExec(ctx context.Context, p ExecConfirmPrompt) (approved bool, err error)
	Choose(ctx context.Context, p UserChoicePrompt) (selected []string, err error)
}

// ChatResult 一轮 chat 结果。
type ChatResult struct {
	Text      string
	Success   bool
	Cancelled bool
	ErrMsg    string
}

// CataConn 与 cata server 的长连接（每个渠道会话一条，保留 server 侧 history）。
type CataConn struct {
	socketPath string
	cwd        string
	runtime    brain.RuntimeEnv

	mu   sync.Mutex
	conn net.Conn
	br   *bufio.Reader
}

// NewCataConn 创建 cata socket 连接句柄。
func NewCataConn(socketPath, cwd string) *CataConn {
	rt := brain.DetectRuntimeEnvFromProcess()
	return &CataConn{socketPath: socketPath, cwd: cwd, runtime: rt}
}

// Close 关闭连接。
func (c *CataConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	c.br = nil
	return err
}

func (c *CataConn) ensureConn() error {
	if c.conn != nil {
		return nil
	}
	conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
	if err != nil {
		return fmt.Errorf("connect cata socket %s: %w", c.socketPath, err)
	}
	c.conn = conn
	c.br = bufio.NewReaderSize(conn, 64*1024)
	return nil
}

func (c *CataConn) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = c.conn.Write(append(b, '\n'))
	return err
}

func (c *CataConn) readLine() ([]byte, error) {
	for {
		line, err := c.br.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		line = bytes.ReplaceAll(line, []byte{0}, nil)
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			return line, nil
		}
	}
}

// Ping 检查 cata server。
func Ping(socketPath string) error {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	req, _ := json.Marshal(map[string]string{"command": "ping"})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return err
	}
	var resp cataResponse
	if err := json.Unmarshal(bytes.TrimSpace(line), &resp); err != nil {
		return err
	}
	if !resp.Success || resp.Message != "pong" {
		return fmt.Errorf("bad ping response")
	}
	return nil
}

// Reset 清空 server 侧会话历史。
func (c *CataConn) Reset() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureConn(); err != nil {
		return err
	}
	if err := c.write(cataRequest{Command: "chat_reset"}); err != nil {
		return err
	}
	line, err := c.readLine()
	if err != nil {
		return err
	}
	var resp cataResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("chat_reset: %s", resp.Message)
	}
	return nil
}

// Chat 发送用户消息并消费 NDJSON 流直到 done。
func (c *CataConn) Chat(ctx context.Context, text string, h StreamHandler) (ChatResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureConn(); err != nil {
		return ChatResult{}, err
	}
	rt := c.runtime
	if err := c.write(cataRequest{
		Command: "chat",
		Text:    text,
		Stream:  true,
		Cwd:     c.cwd,
		Runtime: &rt,
	}); err != nil {
		return ChatResult{}, err
	}

	var out strings.Builder
	for {
		select {
		case <-ctx.Done():
			_ = c.write(cataRequest{Command: "chat_cancel"})
			return ChatResult{Cancelled: true}, ctx.Err()
		default:
		}

		line, err := c.readLine()
		if err != nil {
			return ChatResult{Text: out.String()}, err
		}
		var ev map[string]any
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		typ, _ := ev["type"].(string)
		switch typ {
		case "token":
			if s, ok := ev["content"].(string); ok {
				out.WriteString(s)
			}
		case "progress":
			if h != nil {
				if s, ok := ev["message"].(string); ok {
					h.OnProgress(s)
				}
			}
		case "tool_start":
			if h != nil {
				if s, ok := ev["name"].(string); ok {
					h.OnToolStart(s)
				}
			}
		case "error":
			msg, _ := ev["message"].(string)
			return ChatResult{Text: out.String(), Success: false, ErrMsg: msg}, nil
		case "exec_confirm_required":
			if h == nil {
				return ChatResult{Text: out.String()}, fmt.Errorf("exec_confirm_required but no handler")
			}
			p := ExecConfirmPrompt{
				ConfirmID:   str(ev["confirm_id"]),
				CommandLine: str(ev["command_line"]),
				Cwd:         str(ev["cwd"]),
			}
			ok, err := h.ConfirmExec(ctx, p)
			if err != nil {
				return ChatResult{Text: out.String()}, err
			}
			if err := c.write(cataRequest{
				Command:   "exec_confirm",
				ConfirmID: p.ConfirmID,
				Approved:  ok,
			}); err != nil {
				return ChatResult{Text: out.String()}, err
			}
		case "user_choice":
			if h == nil {
				return ChatResult{Text: out.String()}, fmt.Errorf("user_choice but no handler")
			}
			p := UserChoicePrompt{
				ChoiceID: str(ev["id"]),
				Prompt:   str(ev["prompt"]),
				Multi:    boolVal(ev["multi"]),
				Options:  parseChoiceOptions(ev["options"]),
			}
			selected, err := h.Choose(ctx, p)
			if err != nil {
				return ChatResult{Text: out.String()}, err
			}
			if err := c.write(cataRequest{
				Command:  "user_choice",
				ChoiceID: p.ChoiceID,
				Selected: selected,
			}); err != nil {
				return ChatResult{Text: out.String()}, err
			}
		case "done":
			success, _ := ev["success"].(bool)
			cancelled, _ := ev["cancelled"].(bool)
			return ChatResult{
				Text:      out.String(),
				Success:   success,
				Cancelled: cancelled,
			}, nil
		}
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func boolVal(v any) bool {
	b, _ := v.(bool)
	return b
}

func parseChoiceOptions(raw any) []ChoiceOption {
	arr, _ := raw.([]any)
	var out []ChoiceOption
	for _, r := range arr {
		m, _ := r.(map[string]any)
		out = append(out, ChoiceOption{
			ID:    str(m["id"]),
			Label: str(m["label"]),
			Desc:  str(m["desc"]),
		})
	}
	return out
}

// SplitTelegramMessage 按 Telegram 4096 字符限制切分。
func SplitTelegramMessage(text string, max int) []string {
	if max <= 0 {
		max = 4096
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{"(empty response)"}
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
