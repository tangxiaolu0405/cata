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
	// dialFunc 非 nil 时替代 Unix socket 拨号（远程隧道流等），
	// 使 NDJSON chat 协议可以原样跑在任意 net.Conn 上。
	dialFunc func() (net.Conn, error)

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

// NewConnWithDialer 创建连接句柄，用自定义 dialFunc 拨号（如远程隧道的逻辑连接）。
// socketPath 仅作日志/标识用途。
func NewConnWithDialer(socketPath, cwd string, dialFunc func() (net.Conn, error)) *Conn {
	c := NewConn(socketPath, cwd)
	c.dialFunc = dialFunc
	return c
}

// Cwd 返回该连接绑定的产出区。
func (c *Conn) Cwd() string { return c.cwd }

// DialKey 返回拨号标识："" = 默认 Unix socket；"dialer" = 自定义 dialFunc。
// 供上层判断连接的目标是否变化（如换绑 agent 后须重建，而非复用 cwd 相同的旧连接）。
func (c *Conn) DialKey() string {
	if c.dialFunc != nil {
		return "dialer"
	}
	return ""
}

// Healthy 报告缓存连接是否仍可用（conn 非 nil）。
// 连接在读写错误后由 invalidate 置 nil，因此 nil 即失效（隧道抖动自愈后需重建）。
// 不做在线 ping——保持轻量，避免与进行中的 ChatAs 竞争同一连接。
func (c *Conn) Healthy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

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
	var conn net.Conn
	var err error
	if c.dialFunc != nil {
		conn, err = c.dialFunc()
		if err != nil {
			return fmt.Errorf("dial cata agent: %w", err)
		}
	} else {
		conn, err = net.DialTimeout("unix", c.socketPath, 5*time.Second)
		if err != nil {
			return fmt.Errorf("connect cata socket %s: %w", c.socketPath, err)
		}
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
	if c.conn == nil {
		return fmt.Errorf("not connected")
	}
	_, err = c.conn.Write(append(b, '\n'))
	if err != nil {
		// 写失败说明连接已死：使连接失效，下次 Chat 自动重拨（隧道抖动自愈）。
		c.invalidate()
	}
	return err
}

// invalidate 标记当前连接失效（读写错误后调用），下次 ensureConn 重新拨号。
// 注意：write/readLine 在 ChatAs 持 c.mu 时调用，这里不能再 Lock——
// 只关闭底层 conn 并清引用；若调用方未持锁（Close 路径），先取锁再调用。
func (c *Conn) invalidate() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.conn = nil
	c.br = nil
}

func (c *Conn) invalidateLocked() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.invalidate()
}

func (c *Conn) readLine() ([]byte, error) {
	for {
		line, err := c.br.ReadBytes('\n')
		if err != nil {
			c.invalidate()
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
	return c.ChatAsWithAttachments(ctx, text, runAs, nil, h)
}

// ChatAsWithAttachments 发送用户消息（可带附件）并消费 NDJSON 流直到 done。
// attachments 为 server 端 AttachmentReq（path 相对产出区或附件白名单目录；
// inline 为已编码 base64）。逐条失败的会发 attachment_rejected 事件。
func (c *Conn) ChatAsWithAttachments(ctx context.Context, text, runAs string, attachments []AttachmentReq, h StreamHandler) (ChatResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureConn(); err != nil {
		return ChatResult{}, err
	}
	rt := c.runtime
	if err := c.write(cataRequest{
		Command:     "chat",
		Text:        text,
		Stream:      true,
		Cwd:         c.cwd,
		Runtime:     &rt,
		RunAs:       runAs,
		Attachments: attachments,
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
	Command     string            `json:"command"`
	Text        string            `json:"text,omitempty"`
	Stream      bool              `json:"stream,omitempty"`
	ConfirmID   string            `json:"confirm_id,omitempty"`
	Approved    bool              `json:"approved,omitempty"`
	ChoiceID    string            `json:"choice_id,omitempty"`
	Selected    []string          `json:"selected,omitempty"`
	Cwd         string            `json:"cwd,omitempty"`
	Runtime     *brain.RuntimeEnv `json:"runtime,omitempty"`
	RunAs       string            `json:"run_as,omitempty"`
	Attachments []AttachmentReq   `json:"attachments,omitempty"`
}

// AttachmentReq 单个附件请求：path 与 inline 二选一（与 server 端协议一致）。
type AttachmentReq struct {
	Path   string            `json:"path,omitempty"`
	Inline *InlineAttachment `json:"inline,omitempty"`
}

// InlineAttachment 客户端已编码的附件内容（剪贴板粘贴/渠道下载等场景）。
type InlineAttachment struct {
	MIME   string `json:"mime,omitempty"`
	Base64 string `json:"base64,omitempty"`
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
