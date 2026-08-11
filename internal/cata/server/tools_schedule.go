package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
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

// scheduleInScope 判断某条排程是否属于当前 chat 工作区（chat 管理不跨工作区）：
//   - 项目级排程（project 非空）：只属于该项目根；
//   - 机器级排程：属于创建它的工作区（ws_id 匹配；旧版无 ws_id 时按 cwd 归属判断）。
func scheduleInScope(s *scheduler.Schedule, cc *brain.ChatContext) bool {
	if s == nil || cc == nil {
		return false
	}
	if strings.TrimSpace(s.Project) != "" {
		return cc.WS != nil && cc.WS.RootPath == s.Project
	}
	if s.WSID != "" {
		return cc.WS != nil && cc.WS.ID == s.WSID
	}
	root := ""
	if cc.WS != nil {
		root = cc.WS.RootPath
	}
	if root == "" {
		root = cc.OutputCwd
	}
	root = strings.TrimRight(filepath.Clean(root), string(os.PathSeparator))
	cwd := strings.TrimRight(filepath.Clean(s.Cwd), string(os.PathSeparator))
	return cwd != "" && (cwd == root || strings.HasPrefix(cwd, root+string(os.PathSeparator)))
}

// --- schedule_task ---

type scheduleTaskTool struct{}

func (t *scheduleTaskTool) Name() string { return "schedule_task" }

func (t *scheduleTaskTool) Schema() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "schedule_task",
		Description: "Create or update a self-hosted scheduled task. Exactly one of cron (5 fields: minute hour day month weekday, e.g. \"0 9 * * *\") or interval (e.g. \"24h\", \"30m\") is required. Stored in the current project .cata/schedules (machine ~/.cata/schedules for ephemeral dirs) and discovered by the cata schedule daemon. The task is scoped to this workspace: schedule_list / schedule_remove / schedule_cancel only see tasks created here. At trigger time the daemon acts as a real client and runs a full chat in the task's workspace with browser MCP available; ask_user is auto-skipped and run_command requires allow_exec=true.",
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
		if !config.SchedulesEnabled() {
			daemonNote = "（调度守护未启动：config.schedules.enabled=false）"
		} else if err := ensureSchedulerDaemon(); err != nil {
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
		Description: "List scheduled tasks in the current workspace with id, cron/interval, enabled, next_run, and last run status (tasks never cross workspaces).",
		Parameters:  json.RawMessage(`{"type":"object","properties":{},"required":[]}`),
	}}
}

func (t *scheduleListTool) Execute(ctx context.Context, _ net.Conn, argsJSON string) (string, error) {
	all, err := scheduler.ListAll()
	if err != nil {
		return "", fmt.Errorf("schedule_list: %w", err)
	}
	cc := brain.ChatContextFrom(ctx)
	var in []*scheduler.Schedule
	for _, s := range all {
		if scheduleInScope(s, cc) {
			in = append(in, s)
		}
	}
	if len(in) == 0 {
		return "No scheduled tasks in this workspace. Use schedule_task to create one.", nil
	}
	var b strings.Builder
	b.WriteString("| id | name | schedule | enabled | next_run | last_run |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, s := range in {
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
	found, _, err := scheduler.Find(p.ID)
	if err != nil {
		return "", fmt.Errorf("schedule_remove: %w", err)
	}
	if found == nil {
		return "", fmt.Errorf("schedule_remove: no scheduled task %q", p.ID)
	}
	if !scheduleInScope(found, brain.ChatContextFrom(ctx)) {
		return "", fmt.Errorf("schedule_remove: no scheduled task %q in this workspace", p.ID)
	}
	if err := scheduler.Remove(p.ID); err != nil {
		return "", fmt.Errorf("schedule_remove: %w", err)
	}
	return fmt.Sprintf("schedule_remove: removed %s", p.ID), nil
}

// --- schedule_cancel ---

type scheduleCancelTool struct{}

func (t *scheduleCancelTool) Name() string { return "schedule_cancel" }

func (t *scheduleCancelTool) Schema() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "schedule_cancel",
		Description: "Cancel a scheduled task in the current workspace: disable it so it no longer triggers. The definition is kept; re-enable later with schedule_task using the same name and enabled=true.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","description":"Task id (see schedule_list)"}},"required":["id"]}`),
	}}
}

func (t *scheduleCancelTool) Execute(ctx context.Context, _ net.Conn, argsJSON string) (string, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := llm.ParseToolArguments(argsJSON, &p); err != nil {
		return "", fmt.Errorf("schedule_cancel args: %w", err)
	}
	if strings.TrimSpace(p.ID) == "" {
		return "", fmt.Errorf("schedule_cancel: id required")
	}
	found, _, err := scheduler.Find(p.ID)
	if err != nil {
		return "", fmt.Errorf("schedule_cancel: %w", err)
	}
	if found == nil {
		return "", fmt.Errorf("schedule_cancel: no scheduled task %q", p.ID)
	}
	if !scheduleInScope(found, brain.ChatContextFrom(ctx)) {
		return "", fmt.Errorf("schedule_cancel: no scheduled task %q in this workspace", p.ID)
	}
	if !found.Enabled {
		return fmt.Sprintf("schedule_cancel: %s already cancelled (enabled=false)", p.ID), nil
	}
	found.Enabled = false
	found.NextRun = "" // Save 对 disabled 排程不再重算 next_run
	if err := scheduler.Save(found); err != nil {
		return "", fmt.Errorf("schedule_cancel: %w", err)
	}
	return fmt.Sprintf("schedule_cancel: %s cancelled (enabled=false, no longer triggers); re-enable with schedule_task name=%q enabled=true", p.ID, found.Name), nil
}
