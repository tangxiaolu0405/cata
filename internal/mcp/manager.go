package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"cata/internal/brain"
	"cata/internal/config"
	"cata/internal/llm"
)

type toolRoute struct {
	serverName string
	toolName   string
}

// Manager 管理已连接的 MCP server 与工具路由。
type Manager struct {
	mu        sync.RWMutex
	clients   map[string]*stdioClient
	routes    map[string]*toolRoute
	llmTools  []llm.Tool
	timeout   time.Duration
	maxOutput int

	// server configs for reconnect
	serverCfgs map[string]config.MCPServerEntry
}

var (
	global     *Manager
	initMu     sync.Mutex
	lastMCPKey string
)

// matchesToolPattern returns true if toolName matches a config pattern.
// Patterns support * wildcards (e.g. "browser_*" or "*").
func matchesToolPattern(toolName, pattern string) bool {
	if pattern == "*" {
		return true
	}
	if strings.Contains(pattern, "*") {
		prefix, suffix, _ := strings.Cut(pattern, "*")
		return strings.HasPrefix(toolName, prefix) && strings.HasSuffix(toolName, suffix)
	}
	return toolName == pattern
}

// isToolAllowed checks whether a tool name is in the allowed list (with wildcard support).
func isToolAllowed(name string, allowed []string) bool {
	for _, p := range allowed {
		if matchesToolPattern(name, p) {
			return true
		}
	}
	return false
}

// Init 按配置与 capabilities 启动 MCP；失败的服务器仅记日志。
func Init(cfg config.MCPConfig, caps brain.Capabilities) *Manager {
	mgr := &Manager{
		clients:    make(map[string]*stdioClient),
		routes:     make(map[string]*toolRoute),
		serverCfgs: make(map[string]config.MCPServerEntry),
		timeout:    time.Duration(cfg.ToolTimeoutSeconds) * time.Second,
		maxOutput:  cfg.MaxOutputBytes,
	}
	if !cfg.Enabled {
		global = mgr
		return mgr
	}
	for _, s := range cfg.Servers {
		mgr.serverCfgs[s.Name] = s
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, s := range cfg.Servers {
		if !s.Enabled {
			continue
		}
		if !caps.AllowsMCPServer(s.Name) {
			continue
		}
		if err := connectServer(mgr, ctx, s); err != nil {
			log.Printf("MCP server %q: %v", s.Name, err)
		}
	}
	global = mgr
	if n := len(mgr.llmTools); n > 0 {
		log.Printf("MCP: %d tool(s) from %d server(s)", n, len(mgr.clients))
	}
	return mgr
}

func openStdioServer(ctx context.Context, s config.MCPServerEntry) (*stdioClient, []listedTool, error) {
	c, err := startStdioClient(ctx, s.Name, s.Command, s.Args, s.Env)
	if err != nil {
		return nil, nil, err
	}
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tools, err := c.listTools(listCtx)
	if err != nil {
		_ = c.Close()
		return nil, nil, err
	}
	return c, tools, nil
}

func connectServer(mgr *Manager, ctx context.Context, s config.MCPServerEntry) error {
	c, tools, err := openStdioServer(ctx, s)
	if err != nil {
		return err
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	mgr.clients[s.Name] = c

	allowed := config.Config.MCP.AllowedTools
	maxExport := config.Config.MCP.MaxExportedTools
	exported := 0
	for _, t := range tools {
		name := strings.TrimSpace(t.Name)
		if name == "" || exported >= maxExport {
			continue
		}
		if isToolAllowed(name, allowed) {
			mgr.routes[name] = &toolRoute{serverName: s.Name, toolName: name}
			mgr.llmTools = append(mgr.llmTools, toLLMTool(t))
			exported++
		}
	}
	return nil
}

func mcpCapsKey(caps brain.Capabilities) string {
	parts := make([]string, len(caps.MCP))
	copy(parts, caps.MCP)
	for i := 0; i < len(parts); i++ {
		parts[i] = strings.ToLower(strings.TrimSpace(parts[i]))
	}
	return strings.Join(parts, ",")
}

// EnsureInit 按 capabilities 延迟初始化 MCP；mcp 列表变化时重建。
func EnsureInit() {
	initMu.Lock()
	defer initMu.Unlock()
	if config.Config == nil || !config.Config.MCP.Enabled {
		global = &Manager{clients: make(map[string]*stdioClient), routes: make(map[string]*toolRoute)}
		lastMCPKey = ""
		return
	}
	caps := brain.LoadActiveCapabilities()
	key := mcpCapsKey(caps)
	if global != nil && key == lastMCPKey {
		return
	}
	shutdownLocked()
	Init(config.Config.MCP, caps)
	lastMCPKey = key
}

func shutdownLocked() {
	if global == nil {
		return
	}
	for name, c := range global.clients {
		if err := c.Close(); err != nil {
			log.Printf("MCP close %q: %v", name, err)
		}
	}
	global.clients = nil
	global.routes = nil
	global.llmTools = nil
	global = nil
}

func toLLMTool(t listedTool) llm.Tool {
	params := t.InputSchema
	if len(params) == 0 {
		params = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	desc := strings.TrimSpace(t.Description)
	if desc == "" {
		desc = "MCP tool " + t.Name
	}
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        t.Name,
			Description: desc + " (via MCP)",
			Parameters:  params,
		},
	}
}

// Global 返回已初始化的 MCP 管理器（可能为 nil 或无工具）。
func Global() *Manager {
	EnsureInit()
	return global
}

// Tools 供 LLM API 注册的 MCP 工具列表。
func (mgr *Manager) Tools() []llm.Tool {
	if mgr == nil {
		return nil
	}
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	out := make([]llm.Tool, len(mgr.llmTools))
	copy(out, mgr.llmTools)
	return out
}

// TryCall 若 name 为 MCP 工具则执行并返回 ok=true。
// 永远不向调用者返回 Go error — 浏览器错误作为文本输出，让 LLM 可见。
func (mgr *Manager) TryCall(ctx context.Context, name, argsJSON string) (out string, err error, ok bool) {
	if mgr == nil {
		return "", nil, false
	}
	mgr.mu.RLock()
	route, exists := mgr.routes[name]
	var client *stdioClient
	if exists && route != nil {
		client = mgr.clients[route.serverName]
	}
	mgr.mu.RUnlock()
	if !exists || route == nil || client == nil {
		return "", nil, false
	}
	var args map[string]interface{}
	if strings.TrimSpace(argsJSON) == "" || argsJSON == "null" {
		args = map[string]interface{}{}
	} else if err := llm.ParseToolArguments(argsJSON, &args); err != nil {
		return "", fmt.Errorf("mcp args: %w", err), true
	}
	callCtx := ctx
	if mgr.timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, mgr.timeout)
		defer cancel()
	}
	text, callErr := client.callTool(callCtx, route.toolName, args)

	// Retry once on transient errors: reconnect browser and try again.
	if callErr != nil && isTransientMCPError(callErr) {
		log.Printf("MCP transient error on %s/%s: %v — reconnecting browser", route.serverName, route.toolName, callErr)
		if newClient, reconnErr := mgr.reconnectServer(callCtx, route.serverName, client); reconnErr == nil {
			text, callErr = newClient.callTool(callCtx, route.toolName, args)
		} else {
			log.Printf("MCP reconnect failed: %v", reconnErr)
		}
	}

	// Always return content as text — never lose the actual error to Go error.
	if callErr != nil && text == "" {
		text = "[browser error] " + callErr.Error()
	}
	if mgr.maxOutput > 0 && len(text) > mgr.maxOutput {
		text = text[:mgr.maxOutput] + "\n…(truncated)"
	}
	return text, nil, true
}

// reconnectServer closes the old client and starts a new one for the same server name.
func (mgr *Manager) reconnectServer(ctx context.Context, name string, old *stdioClient) (*stdioClient, error) {
	_ = old.Close()

	mgr.mu.RLock()
	cfg, ok := mgr.serverCfgs[name]
	mgr.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no config for server %q", name)
	}

	c, _, err := openStdioServer(ctx, cfg)
	if err != nil {
		return nil, err
	}

	mgr.mu.Lock()
	mgr.clients[name] = c
	mgr.mu.Unlock()

	log.Printf("MCP server %q reconnected", name)
	return c, nil
}

// isTransientMCPError returns true for errors that can be recovered by reconnecting.
func isTransientMCPError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "eof") ||
		strings.Contains(msg, "process already finished") ||
		strings.Contains(msg, "os: process already finished") ||
		os.IsTimeout(err)
}

// IsBrowserTool reports whether name is an MCP browser tool.
func IsBrowserTool(name string) bool {
	return strings.HasPrefix(name, "browser_")
}

// Shutdown 关闭所有 MCP 子进程。
func Shutdown() {
	initMu.Lock()
	defer initMu.Unlock()
	shutdownLocked()
	lastMCPKey = ""
}
