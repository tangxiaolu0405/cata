package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync/atomic"

	"cata/internal/brain"
	"cata/internal/config"
	"cata/internal/clock"
	"cata/internal/llm"
	"cata/internal/mcp"
)

var subagentSeq uint64

func nextSubagentID() string {
	n := atomic.AddUint64(&subagentSeq, 1)
	return fmt.Sprintf("sub-%d-%d", clock.Now().Unix(), n)
}

const (
	delegateMaxRoundsCap = 16
)

func defaultDelegateMaxRounds() int {
	return config.DefaultSubagentMaxRounds()
}

var workerBuiltinToolNames = map[string]bool{
	"read_file":       true,
	"list_files":      true,
	"read_skill":      true,
	"run_command":     true,
	"search_replace":  true,
	"append_file":     true,
	"create_file":     true,
	"run_skill":       true,
}

var workerExcludedBuiltinTools = map[string]bool{
	"ask_user":      true,
	"delegate_task": true,
	"delegate_wait": true,
}

func (ss *SocketServer) buildWorkerTools() []llm.Tool {
	var out []llm.Tool
	for _, t := range ss.tools.Schemas() {
		name := t.Function.Name
		if workerExcludedBuiltinTools[name] || !workerBuiltinToolNames[name] {
			continue
		}
		out = append(out, t)
	}
	mcp.EnsureInit()
	if mgr := mcp.Global(); mgr != nil {
		out = append(out, mgr.Tools()...)
	}
	return out
}

func (ss *SocketServer) workerToolsCacheKey() string {
	var b strings.Builder
	b.WriteString(mcp.ActiveCapsKey())
	if cfg := config.Config; cfg != nil {
		b.WriteString("|exec:")
		b.WriteString(strconv.FormatBool(cfg.Exec.Enabled))
		b.WriteString("|files:")
		b.WriteString(strconv.FormatBool(cfg.WorkspaceFilesEnabled()))
		b.WriteString("|mcp:")
		b.WriteString(strconv.FormatBool(cfg.MCP.Enabled))
	}
	b.WriteString("|n:")
	b.WriteString(strconv.Itoa(len(ss.tools.Names())))
	return b.String()
}

func (ss *SocketServer) workerTools() []llm.Tool {
	key := ss.workerToolsCacheKey()
	if key == ss.workerToolsKey && len(ss.workerToolsCache) > 0 {
		out := make([]llm.Tool, len(ss.workerToolsCache))
		copy(out, ss.workerToolsCache)
		return out
	}
	out := ss.buildWorkerTools()
	ss.workerToolsKey = key
	ss.workerToolsCache = out
	return out
}

type delegateTaskTool struct {
	ss *SocketServer
}

func (t *delegateTaskTool) Name() string { return "delegate_task" }

func (t *delegateTaskTool) Schema() llm.Tool {
	spec, err := brain.LoadDelegateTaskToolSpec()
	if err != nil {
		spec = brain.DelegateTaskToolSpec{
			Description: "Delegate bounded sub-task to minimal-brain worker.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"task":{"type":"string"}},"required":["task"]}`),
		}
	}
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "delegate_task",
		Description: spec.Description,
		Parameters:  spec.Parameters,
	}}
}

func (t *delegateTaskTool) Execute(ctx context.Context, conn net.Conn, argsJSON string) (string, error) {
	pool := chatSubagentPoolFrom(ctx)
	if pool == nil {
		return "", fmt.Errorf("delegate_task: internal pool missing")
	}
	var p struct {
		Task      string   `json:"task"`
		Context   string   `json:"context"`
		Tools     []string `json:"tools"`
		MaxRounds int      `json:"max_rounds"`
		Wait      bool     `json:"wait"`
	}
	if err := llm.ParseToolArguments(argsJSON, &p); err != nil {
		return "", fmt.Errorf("delegate_task args: %w", err)
	}
	task := strings.TrimSpace(p.Task)
	if task == "" {
		return "", fmt.Errorf("delegate_task: task required")
	}
	maxRounds := clampDelegateRounds(p.MaxRounds)

	id, started, err := pool.Start(ctx, task, p.Context, p.Tools, maxRounds)
	if err != nil {
		return "", err
	}
	started = maybeAppendDelegateHints(started, task, p.Context)
	if !p.Wait {
		return started, nil
	}
	out, err := pool.Wait(ctx, []string{id}, false)
	if err != nil {
		return "", err
	}
	_ = brain.AppendDelegateWaitNote(out)
	return out, nil
}

func clampDelegateRounds(n int) int {
	if n <= 0 {
		return defaultDelegateMaxRounds()
	}
	if n > delegateMaxRoundsCap {
		return delegateMaxRoundsCap
	}
	return n
}

// maybeAppendDelegateHints 父 Agent 委派后附简短提示（minimal worker 依赖 task/context）。
func maybeAppendDelegateHints(started, task, parentContext string) string {
	var hints []string
	if strings.TrimSpace(parentContext) == "" {
		hints = append(hints, "hint: context empty—minimal worker needs file paths, schema, and decisions in context")
	}
	if len(task) > 2500 {
		hints = append(hints, "hint: task is very long—save data to files and reference paths in context")
	}
	if strings.Contains(task, "/mnt/") {
		if env := brain.ActiveRuntimeEnv(); env != nil && !env.ShellSupportsUnixSyntax() {
			hints = append(hints, "hint: task uses /mnt/ paths on non-WSL shell—use output_cwd-relative or native Windows paths")
		}
	}
	if len(hints) == 0 {
		return started
	}
	return started + "\n" + strings.Join(hints, "\n")
}

type delegateWaitTool struct {
	ss *SocketServer
}

func (t *delegateWaitTool) Name() string { return "delegate_wait" }

func (t *delegateWaitTool) Schema() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name: "delegate_wait",
		Description: "Block until sub-agent(s) finish and return summaries. `ids` fetches specific tasks (including already finished in this chat). Omit ids + `all:true` for every task in session; omit both to wait only still-running.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"ids":{"type":"array","items":{"type":"string"},"description":"Sub-agent ids (e.g. sub-1740000000-1). Empty with all=false = running only."},
				"all":{"type":"boolean","description":"If true and ids empty, return summaries for all delegate_task in this chat session"}
			}
		}`),
	}}
}

func (t *delegateWaitTool) Execute(ctx context.Context, conn net.Conn, argsJSON string) (string, error) {
	pool := chatSubagentPoolFrom(ctx)
	if pool == nil {
		return "", fmt.Errorf("delegate_wait: internal pool missing")
	}
	var p struct {
		IDs []string `json:"ids"`
		All bool     `json:"all"`
	}
	if argsJSON != "" && argsJSON != "{}" {
		if err := llm.ParseToolArguments(argsJSON, &p); err != nil {
			return "", fmt.Errorf("delegate_wait args: %w", err)
		}
	}
	out, err := pool.Wait(ctx, p.IDs, p.All)
	if err != nil {
		return "", err
	}
	if err := brain.AppendDelegateWaitNote(out); err != nil {
		log.Printf("delegate_wait short-term: %v", err)
	}
	return out, nil
}
