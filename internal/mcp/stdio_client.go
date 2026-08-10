package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const mcpProtocolVersion = "2024-11-05"

type stdioClient struct {
	name     string
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	reader   *bufio.Reader
	mu       sync.Mutex
	nextID   int
	stderrMu sync.Mutex
	stderrRB *ringBuffer
}

// ringBuffer holds the last N lines of stderr for diagnostics.
type ringBuffer struct {
	buf  []string
	size int
	pos  int
	full bool
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{buf: make([]string, size), size: size}
}

func (rb *ringBuffer) push(line string) {
	rb.buf[rb.pos] = line
	rb.pos = (rb.pos + 1) % rb.size
	if rb.pos == 0 {
		rb.full = true
	}
}

func (rb *ringBuffer) drain() string {
	if rb == nil {
		return ""
	}
	n := rb.pos
	if rb.full {
		n = rb.size
	}
	if n == 0 {
		return ""
	}
	lines := make([]string, 0, n)
	if rb.full {
		lines = append(lines, rb.buf[rb.pos:]...)
	}
	lines = append(lines, rb.buf[:rb.pos]...)
	rb.pos = 0
	rb.full = false
	rb.buf = make([]string, rb.size)
	return strings.Join(lines, "\n")
}

func startStdioClient(ctx context.Context, name, command string, args []string, env map[string]string) (*stdioClient, error) {
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("mcp server %q: empty command", name)
	}
	// 不要用 CommandContext(ctx)：Init/TryCall 的超时 ctx 结束时会 Kill 整个 MCP，
	// 表现为浏览器窗口打开又立刻关掉（闪一下）。进程生命周期由 Close() 管理。
	cmd := exec.Command(command, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), flattenEnv(env)...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start mcp %q: %w", name, err)
	}
	c := &stdioClient{
		name:     name,
		cmd:      cmd,
		stdin:    stdin,
		reader:   bufio.NewReader(stdout),
		stderrRB: newRingBuffer(128), // keep last 128 stderr lines for diagnostics
	}
	go c.drainStderr(stderr)
	initCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := c.initialize(initCtx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func flattenEnv(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

func (c *stdioClient) drainStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		log.Printf("MCP %s stderr: %s", c.name, line)
		c.stderrMu.Lock()
		c.stderrRB.push(line)
		c.stderrMu.Unlock()
	}
}

// fetchStderr drains and returns captured stderr lines, clearing the buffer.
func (c *stdioClient) fetchStderr() string {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	return c.stderrRB.drain()
}

func (c *stdioClient) initialize(ctx context.Context) error {
	var res initializeResult
	if err := c.call(ctx, "initialize", map[string]interface{}{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]string{
			"name":    "cata",
			"version": "0.1",
		},
	}, &res); err != nil {
		return err
	}
	_ = c.notify("notifications/initialized", nil)
	return nil
}

type initializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

type listedTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

func (c *stdioClient) listTools(ctx context.Context) ([]listedTool, error) {
	var res struct {
		Tools []listedTool `json:"tools"`
	}
	if err := c.call(ctx, "tools/list", map[string]interface{}{}, &res); err != nil {
		return nil, err
	}
	return res.Tools, nil
}

func (c *stdioClient) callTool(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
	var res toolCallResult
	if err := c.call(ctx, "tools/call", map[string]interface{}{
		"name":      toolName,
		"arguments": args,
	}, &res); err != nil {
		// Attach captured stderr so the AI can see browser-side diagnostics.
		if diag := c.fetchStderr(); diag != "" {
			return "", fmt.Errorf("%w\n[browser stderr]\n%s", err, diag)
		}
		return "", err
	}
	if res.IsError {
		diag := c.fetchStderr()
		text := "[browser error] " + formatToolContent(res.Content)
		if diag != "" {
			text += "\n[browser stderr]\n" + diag
		}
		return text, nil
	}
	// Clear stderr even on success — only failures matter for diagnostics.
	c.fetchStderr()
	return formatToolContent(res.Content), nil
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolCallResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError"`
}

func formatToolContent(parts []toolContent) string {
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteString("\n")
		}
		switch p.Type {
		case "text", "":
			b.WriteString(p.Text)
		default:
			b.WriteString(fmt.Sprintf("[%s] %s", p.Type, p.Text))
		}
	}
	return b.String()
}

func (c *stdioClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_, _ = c.cmd.Process.Wait()
	}
	return nil
}

func (c *stdioClient) notify(method string, params interface{}) error {
	payload := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		payload["params"] = params
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = c.stdin.Write(data)
	return err
}

func (c *stdioClient) call(ctx context.Context, method string, params interface{}, result interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextID
	c.nextID++
	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := c.stdin.Write(data); err != nil {
		return err
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line, err := c.reader.ReadBytes('\n')
		if err != nil {
			return fmt.Errorf("mcp %q read: %w", c.name, err)
		}
		line = bytesTrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var raw map[string]interface{}
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}
		if _, isMethod := raw["method"]; isMethod {
			continue // notification from server
		}
		if rawID, ok := raw["id"]; ok {
			if !jsonIDEqual(rawID, id) {
				continue
			}
		} else {
			continue
		}
		if errObj, ok := raw["error"]; ok && errObj != nil {
			return fmt.Errorf("mcp %q %s: %v", c.name, method, errObj)
		}
		if result == nil {
			return nil
		}
		resBytes, _ := json.Marshal(raw["result"])
		return json.Unmarshal(resBytes, result)
	}
}

func jsonIDEqual(a interface{}, b int) bool {
	switch v := a.(type) {
	case float64:
		return int(v) == b
	case int:
		return v == b
	default:
		return false
	}
}

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}
