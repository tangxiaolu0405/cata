package pet

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
	"cata/internal/cata/config"
)

type request struct {
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

// StreamEmitter pushes NDJSON-derived events to the UI.
type StreamEmitter interface {
	Emit(event string, data any)
}

// Conn is a long-lived Unix socket session to cata server.
type Conn struct {
	cwd string

	mu     sync.Mutex
	conn   net.Conn
	br     *bufio.Reader
	busy   bool
	cancel context.CancelFunc
}

// NewConn creates a connection handle for the given output cwd.
func NewConn(cwd string) *Conn {
	return &Conn{cwd: cwd}
}

// SetCwd updates the output directory for subsequent chats.
func (c *Conn) SetCwd(cwd string) {
	c.mu.Lock()
	c.cwd = strings.TrimSpace(cwd)
	c.mu.Unlock()
}

// Cwd returns the current output directory.
func (c *Conn) Cwd() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cwd
}

// Close closes the socket.
func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	c.br = nil
	return err
}

func (c *Conn) ensureLocked() error {
	if c.conn != nil {
		return nil
	}
	path := config.ResolvedSocketPath()
	conn, err := net.DialTimeout("unix", path, 5*time.Second)
	if err != nil {
		return fmt.Errorf("connect %s: %w", path, err)
	}
	c.conn = conn
	c.br = bufio.NewReaderSize(conn, 64*1024)
	return nil
}

func (c *Conn) writeLocked(v any) error {
	if c.conn == nil {
		return fmt.Errorf("not connected")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = c.conn.Write(append(b, '\n'))
	return err
}

func (c *Conn) reader() *bufio.Reader {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.br
}

// Busy reports whether a chat stream is in progress.
func (c *Conn) Busy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.busy
}

// Chat starts a streaming chat; emits events via em until done.
func (c *Conn) Chat(text string, em StreamEmitter) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("empty message")
	}

	c.mu.Lock()
	if c.busy {
		c.mu.Unlock()
		return fmt.Errorf("already chatting")
	}
	if err := c.ensureLocked(); err != nil {
		c.mu.Unlock()
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.busy = true
	rt := brain.DetectRuntimeEnvFromProcess()
	cwd := c.cwd
	err := c.writeLocked(request{
		Command: "chat",
		Text:    text,
		Stream:  true,
		Cwd:     cwd,
		Runtime: &rt,
	})
	c.mu.Unlock()
	if err != nil {
		c.finish()
		return err
	}

	em.Emit("pet:mood", "thinking")
	go c.consume(ctx, em)
	return nil
}

func (c *Conn) finish() {
	c.mu.Lock()
	c.busy = false
	c.cancel = nil
	c.mu.Unlock()
}

func (c *Conn) consume(ctx context.Context, em StreamEmitter) {
	defer c.finish()
	defer em.Emit("pet:mood", "idle")

	for {
		select {
		case <-ctx.Done():
			c.mu.Lock()
			_ = c.writeLocked(request{Command: "chat_cancel"})
			c.mu.Unlock()
			em.Emit("pet:done", map[string]any{"cancelled": true})
			return
		default:
		}

		br := c.reader()
		if br == nil {
			em.Emit("pet:error", "disconnected")
			em.Emit("pet:done", map[string]any{"success": false})
			return
		}
		line, err := readNonEmptyLine(br)
		if err != nil {
			em.Emit("pet:error", err.Error())
			em.Emit("pet:done", map[string]any{"success": false})
			return
		}
		var ev map[string]any
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		typ, _ := ev["type"].(string)
		switch typ {
		case "token":
			if s, ok := ev["content"].(string); ok && s != "" {
				em.Emit("pet:token", s)
			}
		case "progress":
			if s, ok := ev["message"].(string); ok {
				em.Emit("pet:progress", s)
			}
		case "tool_start":
			em.Emit("pet:mood", "tool")
			if s, ok := ev["name"].(string); ok {
				em.Emit("pet:tool", s)
			}
		case "error":
			em.Emit("pet:mood", "error")
			msg, _ := ev["message"].(string)
			em.Emit("pet:error", msg)
		case "exec_confirm_required":
			em.Emit("pet:mood", "confirm")
			em.Emit("pet:confirm", map[string]any{
				"confirm_id":   str(ev["confirm_id"]),
				"command_line": str(ev["command_line"]),
				"cwd":          str(ev["cwd"]),
			})
		case "user_choice":
			em.Emit("pet:mood", "confirm")
			em.Emit("pet:choice", ev)
		case "done":
			success, _ := ev["success"].(bool)
			cancelled, _ := ev["cancelled"].(bool)
			em.Emit("pet:done", map[string]any{
				"success":   success,
				"cancelled": cancelled,
			})
			return
		}
	}
}

func readNonEmptyLine(br *bufio.Reader) ([]byte, error) {
	for {
		line, err := br.ReadBytes('\n')
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

// RespondExec answers an exec_confirm_required prompt.
func (c *Conn) RespondExec(confirmID string, approved bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureLocked(); err != nil {
		return err
	}
	return c.writeLocked(request{
		Command:   "exec_confirm",
		ConfirmID: confirmID,
		Approved:  approved,
	})
}

// RespondChoice answers a user_choice prompt.
func (c *Conn) RespondChoice(choiceID string, selected []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureLocked(); err != nil {
		return err
	}
	return c.writeLocked(request{
		Command:  "user_choice",
		ChoiceID: choiceID,
		Selected: selected,
	})
}

// Cancel aborts the current stream if any.
// 直接发送 chat_cancel（不经 consume 的阻塞读循环）——consume 阻塞在 readNonEmptyLine
// 时 select 检查不到 ctx.Done，服务端静默（LLM 思考中）时取消会延迟到下一行。
// 这里取消 ctx 后立即写 chat_cancel，让服务端马上中断本轮。
func (c *Conn) Cancel() {
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	c.mu.Lock()
	if c.busy && c.conn != nil {
		_ = c.writeLocked(request{Command: "chat_cancel"})
	}
	c.mu.Unlock()
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
