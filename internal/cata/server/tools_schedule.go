package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"

	"cata/internal/cata/brain"
	"cata/internal/cata/scheduler"
	"cata/internal/llm"
)

// ensureSchedulerDaemon 创建/启用任务后确保调度守护进程在后台运行（可被测试替换为 no-op）。
// 守护由 scheduler.EnsureDaemonRunning 拉起（单例 socket 锁 + setsid 后台进程），
// 实现「chat 里指定、之后不用管、后台自动执行」。
var ensureSchedulerDaemon = func() error {
	_, err := scheduler.EnsureDaemonRunning()
	return err
}

// --- schedule_task ---

type scheduleTaskTool struct{}

func (t *scheduleTaskTool) Name() string { return "schedule_task" }

func (t *scheduleTaskTool) Schema() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "schedule_task",
		Description: "Create or update a self-hosted scheduled task. Exactly one of cron (5 fields: minute hour day month weekday, e.g. \"0 9 * * *\") or interval (e.g. \"24h\", \"30m\") is required. Stored in the current project .cata/schedules (machine ~/.cata/schedules for ephemeral dirs) and discovered by the cata schedule daemon. At trigger time the daemon acts as a real client and runs a full chat in the current workspace with browser MCP available; ask_user is auto-skipped and run_command requires allow_exec=true.",
		Parameters: json.RawMessage(`{"type":"object","properties":{
			"name":{"type":"string","description":"Task name (stable id derived from it)"},
			"prompt":{"type":"string","description":"Instruction executed when the task fires, e.g. daily product research on a marketplace"},
			"cron":{"type":"string","description":"5-field cron, e.g. \"0 9 * * *\" = daily 09:00"},
			"interval":{"type":"string","description":"Simple interval duration, e.g. \"24h\" or \"30m\" (mutually exclusive with cron)"},
			"output_dir":{"type":"string","description":"Optional absolute directory for run reports (default: <project>/.cata/schedule-runs/<id>)"},
			"allow_exec":{"type":"boolean","description":"If true scheduled runs may execute terminal commands without confirmation (default false)"},
			"enabled":{"type":"boolean","description":"If false create disabled (default true)"}
		},"required":["name","prompt"]}`),
	}}
}

func (t *scheduleTaskTool) Execute(ctx context.Context, _ net.Conn, argsJSON string) (string, error) {
	var p struct {
		Name      string `json:"name"`
		Prompt    string `json:"prompt"`
		Cron      string `json:"cron"`
		Interval  string `json:"interval"`
		OutputDir string `json:"output_dir"`
		AllowExec bool   `json:"allow_exec"`
		Enabled   *bool  `json:"enabled"`
	}
	if err := llm.ParseToolArguments(argsJSON, &p); err != nil {
		return "", fmt.Errorf("schedule_task args: %w", err)
	}
	cc := brain.ChatContextFrom(ctx)
	if strings.TrimSpace(cc.OutputCwd) == "" {
		return "", fmt.Errorf("schedule_task: no output cwd in context")
	}
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	id := scheduler.IDFromName(p.Name)
	if id == "" {
		return "", fmt.Errorf("schedule_task: name must contain letters or digits")
	}
	existing, err := scheduler.Load(id)
	if err != nil {
		return "", fmt.Errorf("schedule_task: load: %w", err)
	}
	wsID := ""
	project := ""
	if cc.WS != nil {
		wsID = cc.WS.ID
		// 项目级排程随项目 .cata 分发（与 MCP/modes/skills 同源）；临时目录退回机器级。
		if cc.WS.Kind == brain.KindGit || cc.WS.Kind == brain.KindMarked {
			project = cc.WS.RootPath
		}
	}
	if existing != nil && existing.Project != "" {
		project = existing.Project // 更新时保留原存储位置，避免同 id 分裂成两条
	}
	s := &scheduler.Schedule{
		ID:        id,
		Name:      p.Name,
		Prompt:    p.Prompt,
		Cron:      p.Cron,
		Interval:  p.Interval,
		Cwd:       cc.OutputCwd,
		WSID:      wsID,
		AllowExec: p.AllowExec,
		Enabled:   enabled,
		OutputDir: p.OutputDir,
		Project:   project,
	}
	if existing != nil {
		s.CreatedAt = existing.CreatedAt
		s.LastRun = existing.LastRun
	}
	s.NextRun = "" // Save 会按当前时间重算
	if err := scheduler.Save(s); err != nil {
		return "", fmt.Errorf("schedule_task: %w", err)
	}
	daemonNote := ""
	if enabled {
		if err := ensureSchedulerDaemon(); err != nil {
			log.Printf("schedule_task: ensure scheduler daemon: %v", err)
		} else {
			daemonNote = "（调度守护已在后台运行）"
		}
	}
	return fmt.Sprintf("schedule_task: %s id=%s next_run=%s cwd=%s (allow_exec=%t)%s", s.Name, s.ID, s.NextRun, s.Cwd, s.AllowExec, daemonNote), nil
}

// --- schedule_list ---

type scheduleListTool struct{}

func (t *scheduleListTool) Name() string { return "schedule_list" }

func (t *scheduleListTool) Schema() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "schedule_list",
		Description: "List all scheduled tasks with id, cron/interval, enabled, next_run, and last run status.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{},"required":[]}`),
	}}
}

func (t *scheduleListTool) Execute(ctx context.Context, _ net.Conn, argsJSON string) (string, error) {
	all, err := scheduler.ListAll()
	if err != nil {
		return "", fmt.Errorf("schedule_list: %w", err)
	}
	if len(all) == 0 {
		return "No scheduled tasks. Use schedule_task to create one.", nil
	}
	var b strings.Builder
	b.WriteString("| id | name | schedule | enabled | next_run | last_run |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, s := range all {
		sched := s.Cron
		if sched == "" {
			sched = "every " + s.Interval
		}
		last := "-"
		if s.LastRun != nil {
			last = s.LastRun.At
			if !s.LastRun.Success {
				last += " (failed)"
			}
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %t | %s | %s |\n", s.ID, s.Name, sched, s.Enabled, s.NextRun, last)
	}
	return b.String(), nil
}

// --- schedule_remove ---

type scheduleRemoveTool struct{}

func (t *scheduleRemoveTool) Name() string { return "schedule_remove" }

func (t *scheduleRemoveTool) Schema() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "schedule_remove",
		Description: "Remove a scheduled task by id (see schedule_list for ids).",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),
	}}
}

func (t *scheduleRemoveTool) Execute(ctx context.Context, _ net.Conn, argsJSON string) (string, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := llm.ParseToolArguments(argsJSON, &p); err != nil {
		return "", fmt.Errorf("schedule_remove args: %w", err)
	}
	if strings.TrimSpace(p.ID) == "" {
		return "", fmt.Errorf("schedule_remove: id required")
	}
	if err := scheduler.Remove(p.ID); err != nil {
		return "", fmt.Errorf("schedule_remove: %w", err)
	}
	return fmt.Sprintf("schedule_remove: removed %s", p.ID), nil
}
