// Package socketclient 提供与 cata server 交互的真实客户端协议（Unix socket + NDJSON）。
// 供 gateway（渠道适配）与调度框架（定时任务自发起）共用；与 TUI 客户端解耦，避免命名冲突。
package socketclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"cata/internal/cata/brain"
)

// Conn 与 cata server 的 Unix socket 客户端。每个 Conn 一条长连接，保留 server 侧 history；
// Chat 消费 NDJSON 流直到 done，并自动应答 exec_confirm_required / user_choice。
type Conn struct {
	socketPath string
	cwd        string
	runtime    brain.RuntimeEnv

	mu    sync.Mutex
	conn  net.Conn
	br    *bufio.Reader
	audit io.Writer // 非 nil 时，每个收到的原始 NDJSON 行同步写入（调度审计用）
}

// NewConn 创建 cata socket 连接句柄。
func NewConn(socketPath, cwd string) *Conn {
	rt := brain.DetectRuntimeEnvFromProcess()
	return &Conn{socketPath: socketPath, cwd: cwd, runtime: rt}
}

// Cwd 返回该连接绑定的产出区。
func (c *Conn) Cwd() string { return c.cwd }

// SetAuditWriter 设置原始 NDJSON 审计写入器（每个收到的行原样写入）。
func (c *Conn) SetAuditWriter(w io.Writer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.audit = w
}

// Close 关闭连接。
func (c *Conn) Close() error {
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

func (c *Conn) ensureConn() error {
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

func (c *Conn) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = c.conn.Write(append(b, '\n'))
	return err
}

func (c *Conn) readLine() ([]byte, error) {
	for {
		line, err := c.br.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		line = bytes.ReplaceAll(line, []byte{0}, nil)
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			if c.audit != nil {
				_, _ = c.audit.Write(line)
				_, _ = c.audit.Write([]byte("\n"))
			}
			return line, nil
		}
	}
}

// Ping 检查 cata server 是否可用。
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
func (c *Conn) Reset() error {
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

// Chat 发送用户消息并消费 NDJSON 流直到 done（普通对话，run_as 为空）。
func (c *Conn) Chat(ctx context.Context, text string, h StreamHandler) (ChatResult, error) {
	return c.ChatAs(ctx, text, "", h)
}

// ChatAs 发送用户消息并消费 NDJSON 流直到 done。
// runAs 传给 server（"" = 普通对话；"scheduled" = 定时任务，server 强制 full 工具档并跳过任务状态机）。
func (c *Conn) ChatAs(ctx context.Context, text, runAs string, h StreamHandler) (ChatResult, error) {
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
		RunAs:   runAs,
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
				if th, ok := any(h).(interface{ OnToken(string) }); ok && th != nil {
					th.OnToken(s)
				}
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
	RunAs     string            `json:"run_as,omitempty"`
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
