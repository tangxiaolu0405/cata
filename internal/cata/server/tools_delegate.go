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

	"cata/internal/cata/brain"
	"cata/internal/cata/clock"
	"cata/internal/cata/config"
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
	"read_file":      true,
	"list_files":     true,
	"read_skill":     true,
	"run_command":    true,
	"search_replace": true,
	"append_file":    true,
	"create_file":    true,
	"run_skill":      true,
	"case_artifact":  true,
}

var workerExcludedBuiltinTools = map[string]bool{
	"ask_user":      true,
	"delegate_task": true,
	"delegate_wait": true,
	"delegate_mode": true,
	"list_modes":    true,
	// case_artifact allowed for mode workers writing drafts
}

// buildWorkerToolsFor 显式指定 workspace/产出区/运行环境的 worker 工具集
// （多 chat 并行勿依赖全局 OutputCwd/RuntimeEnv/Active）。
// run_command 的说明内嵌产出区与 shell 提示，必须按本轮 chat 重建，不能复用全局 Schema()；
// MCP 工具按本轮 workspace capabilities 过滤（per-chat 快照）。
func (ss *SocketServer) buildWorkerToolsFor(ws *brain.Workspace, out string, env *brain.RuntimeEnv) []llm.Tool {
	var outTools []llm.Tool
	for _, t := range ss.tools.Schemas() {
		name := t.Function.Name
		if workerExcludedBuiltinTools[name] || !workerBuiltinToolNames[name] {
			continue
		}
		if name == "run_command" {
			schema := t
			schema.Function.Description = brain.RunCommandToolDescriptionFor(env, out)
			outTools = append(outTools, schema)
			continue
		}
		outTools = append(outTools, t)
	}
	mcp.EnsureInit()
	if mgr := mcp.Global(); mgr != nil {
		if ws != nil {
			outTools = append(outTools, mgr.ToolsFor(brain.LoadCapabilitiesCachedFor(ws))...)
		} else {
			outTools = append(outTools, mgr.Tools()...)
		}
	}
	return outTools
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

// workerToolsFor 显式指定 workspace/产出区/运行环境的 worker 工具集（带缓存；key 含 ws caps/out/env）。
func (ss *SocketServer) workerToolsFor(ws *brain.Workspace, out string, env *brain.RuntimeEnv) []llm.Tool {
	key := ss.workerToolsCacheKey()
	if ws != nil {
		key += "|caps:" + mcp.CapsKey(brain.LoadCapabilitiesCachedFor(ws))
	}
	if out != "" {
		key += "|out:" + out
	}
	if env != nil {
		key += "|env:" + env.OS + "/" + env.Shell
	}
	// 缓存读写加锁：多 chat goroutine 并发构建 worker 工具集时保证 key/cache 成对一致。
	ss.toolsCacheMu.Lock()
	defer ss.toolsCacheMu.Unlock()
	if key == ss.workerToolsKey && len(ss.workerToolsCache) > 0 {
		outTools := make([]llm.Tool, len(ss.workerToolsCache))
		copy(outTools, ss.workerToolsCache)
		return outTools
	}
	outTools := ss.buildWorkerToolsFor(ws, out, env)
	ss.workerToolsKey = key
	ss.workerToolsCache = outTools
	return outTools
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
		Task           string   `json:"task"`
		Context        string   `json:"context"`
		ModeID         string   `json:"mode_id"`
		CaseID         string   `json:"case_id"`
		ReadArtifacts  []string `json:"read_artifacts"`
		WriteArtifacts []string `json:"write_artifacts"`
		Tools          []string `json:"tools"`
		MaxRounds      int      `json:"max_rounds"`
		Wait           bool     `json:"wait"`
	}
	if err := llm.ParseToolArguments(argsJSON, &p); err != nil {
		return "", fmt.Errorf("delegate_task args: %w", err)
	}
	task := strings.TrimSpace(p.Task)
	if task == "" {
		return "", fmt.Errorf("delegate_task: task required")
	}
	if modeID := strings.TrimSpace(p.ModeID); modeID != "" {
		return startModeDelegate(ctx, pool, modeDelegateArgs{
			ModeID:         modeID,
			CaseID:         p.CaseID,
			Task:           task,
			Context:        p.Context,
			ReadArtifacts:  p.ReadArtifacts,
			WriteArtifacts: p.WriteArtifacts,
			Tools:          p.Tools,
			MaxRounds:      p.MaxRounds,
			Wait:           p.Wait,
		})
	}
	maxRounds := clampDelegateRounds(p.MaxRounds)

	id, started, err := pool.Start(ctx, task, p.Context, p.Tools, maxRounds)
	if err != nil {
		return "", err
	}
	started = maybeAppendDelegateHintsFor(ctx, started, task, p.Context)
	if !p.Wait {
		return started, nil
	}
	out, err := pool.Wait(ctx, []string{id}, false)
	if err != nil {
		return "", err
	}
	cc := brain.ChatContextFrom(ctx)
	if err := brain.AppendDelegateWaitNoteFor(cc.WS, out); err != nil {
		log.Printf("delegate_wait short-term: %v", err)
	}
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
	return maybeAppendDelegateHintsFor(context.Background(), started, task, parentContext)
}

// maybeAppendDelegateHintsFor 显式从 ctx 取运行环境（多 chat 并行勿依赖全局 ActiveRuntimeEnv）。
func maybeAppendDelegateHintsFor(ctx context.Context, started, task, parentContext string) string {
	var hints []string
	if strings.TrimSpace(parentContext) == "" {
		hints = append(hints, "hint: context empty—minimal worker needs file paths, schema, and decisions in context")
	}
	if len(task) > 2500 {
		hints = append(hints, "hint: task is very long—save data to files and reference paths in context")
	}
	if strings.Contains(task, "/mnt/") {
		cc := brain.ChatContextFrom(ctx)
		if env := cc.Runtime; env != nil && !env.ShellSupportsUnixSyntax() {
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
		Name:        "delegate_wait",
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
	cc := brain.ChatContextFrom(ctx)
	if err := brain.AppendDelegateWaitNoteFor(cc.WS, out); err != nil {
		log.Printf("delegate_wait short-term: %v", err)
	}
	return out, nil
}
