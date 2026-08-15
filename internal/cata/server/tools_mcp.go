package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strings"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
	"cata/internal/cata/protocol"
	"cata/internal/llm"
	"cata/internal/mcp"
)

// manageMCPTool 让 chat 内 LLM 自主管理 MCP：
//   - install：写全局定义（~/.cata/config.json mcp.servers）+ 当前项目启用；写全局前须用户确认
//   - enable / disable：只改当前项目 capabilities.yaml 的 mcp 段（cata 正常写入区，不需确认）
//   - list：只读列出现状
//
// 决策规则（与 brain/constraints.md §MCP 一致）：
//   - 全局已有同名定义 → 只启用当前项目，不重复写全局
//   - 跨项目通用 / 绑用户本机（browser、全局 CLI）→ 全局定义 + 项目启用
//   - 绑项目数据源/内部服务 → 仍走全局定义（无项目级定义机制），项目启用
//   - 不确定 → 默认全局定义 + 项目启用
type manageMCPTool struct{ ss *SocketServer }

func (t *manageMCPTool) Name() string { return "manage_mcp" }

func (t *manageMCPTool) Schema() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name: "manage_mcp",
		Description: "Manage MCP servers. " +
			"install: add a global definition in ~/.cata/config.json mcp.servers AND enable it in the current project .cata capabilities.yaml (asks the user to confirm before touching global config). " +
			"enable/disable: toggle only the current project's capabilities.yaml mcp list (no confirmation needed; global definition must already exist for enable). " +
			"list: read-only overview of global definitions + current project enabled servers. " +
			"Decision rules: if the server name already exists globally, only enable it in the current project (do NOT duplicate the global definition); " +
			"general/machine-bound servers (e.g. browser) install globally once then enable per project; " +
			"project-specific servers still use the global definition (cata has no per-project server definitions) and are enabled per project; " +
			"when unsure, install globally + enable in the current project.",
		Parameters: json.RawMessage(`{"type":"object","properties":{
			"action":{"type":"string","enum":["install","enable","disable","list"],"description":"install=global definition+project enable (confirm); enable/disable=project only; list=read-only"},
			"server":{"type":"string","description":"MCP server name (e.g. browser)"},
			"command":{"type":"string","description":"install: executable command (default npx)"},
			"args":{"type":"array","items":{"type":"string"},"description":"install: command args (e.g. -y @playwright/mcp@0.0.75)"},
			"env":{"type":"object","additionalProperties":{"type":"string"},"description":"install: extra env vars (optional)"}
		},"required":["action"]}`),
	}}
}

func (t *manageMCPTool) Execute(ctx context.Context, conn net.Conn, argsJSON string) (string, error) {
	var p struct {
		Action  string            `json:"action"`
		Server  string            `json:"server"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	if err := llm.ParseToolArguments(argsJSON, &p); err != nil {
		return "", fmt.Errorf("manage_mcp args: %w", err)
	}
	p.Action = strings.ToLower(strings.TrimSpace(p.Action))
	p.Server = strings.TrimSpace(p.Server)
	switch p.Action {
	case "list":
		return t.mcpList(ctx)
	case "install":
		return t.mcpInstall(ctx, conn, p.Server, p.Command, p.Args, p.Env)
	case "enable":
		return t.mcpEnable(ctx, p.Server)
	case "disable":
		return t.mcpDisable(ctx, p.Server)
	default:
		return "", fmt.Errorf("manage_mcp: unknown action %q (install|enable|disable|list)", p.Action)
	}
}

func (t *manageMCPTool) mcpList(ctx context.Context) (string, error) {
	var b strings.Builder
	b.WriteString("[manage_mcp list]\n")
	if config.Config == nil {
		b.WriteString("config not loaded\n")
		return b.String(), nil
	}
	b.WriteString("global definitions (~/.cata/config.json mcp.servers):\n")
	if len(config.Config.MCP.Servers) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, s := range config.Config.MCP.Servers {
		state := "enabled"
		if !s.Enabled {
			state = "disabled"
		}
		b.WriteString(fmt.Sprintf("  - %s [%s] %s %s\n", s.Name, state, s.Command, strings.Join(s.Args, " ")))
	}
	b.WriteString("current project enabled (capabilities.yaml mcp):\n")
	ws := chatWorkspaceFrom(ctx)
	caps := brain.LoadCapabilitiesFor(ws)
	if len(caps.MCP) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, m := range caps.MCP {
		b.WriteString("  - " + m + "\n")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// mcpInstall 写全局定义 + 项目启用；涉及全局 config.json 前必须用户确认。
func (t *manageMCPTool) mcpInstall(ctx context.Context, conn net.Conn, name, command string, args []string, env map[string]string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("manage_mcp install: server name required")
	}
	if config.Config == nil {
		return "", fmt.Errorf("config not loaded")
	}
	if existing := config.FindMCPServer(config.Config, name); existing != nil {
		// 规则：全局已有同名定义 → 只启用项目，不重复写全局。
		if !existing.Enabled {
			ok, err := t.confirmInstall(ctx, conn, name, existing.Command, existing.Args)
			if err != nil {
				return "", err
			}
			if !ok {
				return fmt.Sprintf("[manage_mcp] install cancelled for %q", name), nil
			}
			copyCfg := cloneAppConfigForMCP()
			if s := config.FindMCPServer(copyCfg, name); s != nil {
				s.Enabled = true
			}
			if err := saveAndReloadConfig(copyCfg); err != nil {
				return "", err
			}
		}
		return t.enableForProject(ctx, name)
	}

	if strings.TrimSpace(command) == "" {
		command = "npx"
	}
	if len(args) == 0 {
		args = []string{"-y", "@playwright/mcp@" + config.DefaultPlaywrightMCPVersion}
	}
	ok, err := t.confirmInstall(ctx, conn, name, command, args)
	if err != nil {
		return "", err
	}
	if !ok {
		return fmt.Sprintf("[manage_mcp] install cancelled for %q", name), nil
	}

	if _, err := exec.LookPath(command); err != nil {
		log.Printf("[mcp-manage] install %q: command %q not on PATH: %v", name, command, err)
	}

	copyCfg := cloneAppConfigForMCP()
	copyCfg.MCP.Enabled = true
	config.UpsertMCPServer(copyCfg, config.MCPServerEntry{
		Name: name, Enabled: true, Command: command, Args: args, Env: env,
	})
	if err := saveAndReloadConfig(copyCfg); err != nil {
		return "", err
	}
	auditMCPAction("install", name)
	return t.enableForProject(ctx, name)
}

func (t *manageMCPTool) mcpEnable(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("manage_mcp enable: server name required")
	}
	if config.Config == nil {
		return "", fmt.Errorf("config not loaded")
	}
	if config.FindMCPServer(config.Config, name) == nil {
		return "", fmt.Errorf("manage_mcp enable: %q has no global definition in ~/.cata/config.json mcp.servers; use action=install first", name)
	}
	out, err := t.enableForProject(ctx, name)
	if err == nil {
		auditMCPAction("enable", name)
	}
	return out, err
}

func (t *manageMCPTool) mcpDisable(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("manage_mcp disable: server name required")
	}
	ws, err := requireChatWorkspace(ctx, "manage_mcp")
	if err != nil {
		return "", err
	}
	caps := brain.LoadCapabilitiesFor(ws)
	if !caps.AllowsMCPServer(name) {
		return fmt.Sprintf("[manage_mcp] %q not enabled in current project", name), nil
	}
	if err := brain.RemoveMCPFromCapabilities(ws, name); err != nil {
		return "", err
	}
	// 项目 caps 变更只影响 ToolsFor 过滤；子进程生命周期与 workspace 解耦，无需重建。
	auditMCPAction("disable", name)
	return fmt.Sprintf("[manage_mcp] disabled %q in current project; next chat round uses updated tools", name), nil
}

// enableForProject 在项目 capabilities.yaml 启用 server 名并重建 MCP（幂等）。
func (t *manageMCPTool) enableForProject(ctx context.Context, name string) (string, error) {
	ws, err := requireChatWorkspace(ctx, "manage_mcp")
	if err != nil {
		return "", err
	}
	caps := brain.LoadCapabilitiesFor(ws)
	if caps.AllowsMCPServer(name) {
		return fmt.Sprintf("[manage_mcp] %q already enabled in current project", name), nil
	}
	if err := brain.AppendMCPToCapabilities(ws, name); err != nil {
		return "", err
	}
	// enable 一个已定义 server：确保其 stdio 子进程已按配置启动（幂等），
	// 项目 caps 过滤由下一轮 ToolsFor 生效。
	mcp.EnsureInit()
	return fmt.Sprintf("[manage_mcp] enabled %q in current project; next chat round uses updated tools", name), nil
}

// requireChatWorkspace 返回本轮 chat 的脑子分区；缺失时返回错误。
// 这些工具只在 chat 工具循环内执行（ctx 必注入 ws），禁止回退到进程级全局
// brain.Active()——后台 evolve 会临时改写全局，多 chat 并行时会写错 workspace。
func requireChatWorkspace(ctx context.Context, toolName string) (*brain.Workspace, error) {
	ws := chatWorkspaceFrom(ctx)
	if ws == nil {
		return nil, fmt.Errorf("%s: chat workspace missing from context (parallel-safe path required)", toolName)
	}
	return ws, nil
}

// confirmInstall 复用 exec_confirm 机制向用户确认全局写入/启动。
func (t *manageMCPTool) confirmInstall(ctx context.Context, conn net.Conn, name, command string, args []string) (bool, error) {
	cmdLine := command
	if len(args) > 0 {
		cmdLine += " " + strings.Join(args, " ")
	}
	id := protocol.NewConfirmID()
	_ = t.ss.emitStreamLine(conn, map[string]interface{}{
		"type":         "exec_confirm_required",
		"title":        "MCP install 待确认 (↑↓ 选 Run/Cancel，Enter 确认，Esc 取消)",
		"confirm_id":   id,
		"argv":         append([]string{command}, args...),
		"command_line": cmdLine,
		"cwd":          config.CataHome(),
		"options": []map[string]string{
			{"id": "run", "label": "Install"},
			{"id": "cancel", "label": "Cancel"},
		},
	})
	approved, err := protocol.WaitExecClientConfirm(ctx, id)
	if err != nil {
		if ctx.Err() != nil {
			return false, nil
		}
		return false, err
	}
	return approved, nil
}

// cloneAppConfigForMCP 复制全局配置（仅深拷贝 MCP 段），避免并发写全局指针。
func cloneAppConfigForMCP() *config.AppConfig {
	if config.Config == nil {
		return nil
	}
	c := *config.Config
	c.MCP.Servers = make([]config.MCPServerEntry, len(config.Config.MCP.Servers))
	for i, s := range config.Config.MCP.Servers {
		ns := s
		if s.Args != nil {
			ns.Args = append([]string(nil), s.Args...)
		}
		if s.Env != nil {
			ns.Env = make(map[string]string, len(s.Env))
			for k, v := range s.Env {
				ns.Env[k] = v
			}
		}
		c.MCP.Servers[i] = ns
	}
	return &c
}

func saveAndReloadConfig(c *config.AppConfig) error {
	if c == nil {
		return fmt.Errorf("config not loaded")
	}
	if err := config.SaveConfig(c); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	if _, err := config.LoadConfig(); err != nil {
		return fmt.Errorf("reload config: %w", err)
	}
	return nil
}

// auditMCPAction 审计：server 日志 + 工具结果已进对话 history/short-term memory。
func auditMCPAction(action, server string) {
	log.Printf("[mcp-manage] action=%s server=%q by=chat", action, server)
}
