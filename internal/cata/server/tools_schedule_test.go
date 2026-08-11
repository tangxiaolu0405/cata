package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
	"cata/internal/cata/scheduler"
)

// scheduleTestContext 用临时 CATA_HOME + 临时 workspace 构造 schedule 工具测试环境。
// 测试里把 ensureSchedulerDaemon 换成 no-op（避免真拉起调度守护/递归执行测试二进制）。
func scheduleTestContext(t *testing.T) *brain.Workspace {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)

	root := t.TempDir()
	ws := &brain.Workspace{ID: "ws", RootPath: root, ActiveMode: brain.ModeDefaultID}
	if err := os.MkdirAll(ws.ModeDir(brain.ModeDefaultID), 0755); err != nil {
		t.Fatal(err)
	}
	old := ensureSchedulerDaemon
	ensureSchedulerDaemon = func() error { return nil }
	t.Cleanup(func() { ensureSchedulerDaemon = old })
	return ws
}

func scheduleCtx(ws *brain.Workspace) context.Context {
	return brain.WithChatContext(context.Background(), &brain.ChatContext{WS: ws, OutputCwd: ws.RootPath})
}

func TestScheduleTaskCreatesCJKName(t *testing.T) {
	ws := scheduleTestContext(t)
	tool := &scheduleTaskTool{}
	out, err := tool.Execute(scheduleCtx(ws), nil, `{"name":"每日选品","prompt":"去电商平台看今日热门商品并整理候选清单","interval":"24h"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "每日选品") {
		t.Fatalf("output should mention name: %s", out)
	}
	s, err := scheduler.Load("每日选品")
	if err != nil || s == nil {
		t.Fatalf("Load(%q) = (%v, %v), want schedule", "每日选品", s, err)
	}
	if s.Cwd != ws.RootPath {
		t.Fatalf("cwd = %q, want %q", s.Cwd, ws.RootPath)
	}
	if s.WSID != "ws" {
		t.Fatalf("ws_id = %q, want ws", s.WSID)
	}
	if !s.Enabled {
		t.Fatal("created schedule should default enabled")
	}
	if s.Interval != "24h" || s.Cron != "" {
		t.Fatalf("schedule fields mismatch: interval=%q cron=%q", s.Interval, s.Cron)
	}
	if s.NextRun == "" {
		t.Fatal("next_run should be computed")
	}
	if s.AllowExec {
		t.Fatal("allow_exec should default false")
	}
	// 落盘在 CATA_HOME/schedules/<id>.json。
	if got := filepath.Join(config.CataHome(), "schedules", "每日选品.json"); !fileExists(got) {
		t.Fatalf("expected schedule file at %s", got)
	}
}

func TestScheduleTaskCronAndOptions(t *testing.T) {
	ws := scheduleTestContext(t)
	tool := &scheduleTaskTool{}
	_, err := tool.Execute(scheduleCtx(ws), nil, `{"name":"daily-report","prompt":"写日报","cron":"0 9 * * *","allow_exec":true,"enabled":false}`)
	if err != nil {
		t.Fatal(err)
	}
	s, err := scheduler.Load("daily-report")
	if err != nil || s == nil {
		t.Fatalf("Load = (%v, %v)", s, err)
	}
	if s.Cron != "0 9 * * *" || s.Interval != "" {
		t.Fatalf("cron=%q interval=%q", s.Cron, s.Interval)
	}
	if !s.AllowExec {
		t.Fatal("allow_exec should be true")
	}
	if s.Enabled {
		t.Fatal("enabled should be false")
	}
}

func TestScheduleTaskUpdatePreservesRunInfo(t *testing.T) {
	ws := scheduleTestContext(t)
	tool := &scheduleTaskTool{}
	if _, err := tool.Execute(scheduleCtx(ws), nil, `{"name":"task","prompt":"v1","interval":"24h"}`); err != nil {
		t.Fatal(err)
	}
	s, err := scheduler.Load("task")
	if err != nil || s == nil {
		t.Fatalf("Load = (%v, %v)", s, err)
	}
	s.LastRun = &scheduler.RunInfo{At: "2026-08-11T10:00:00+08:00", Success: true, Summary: "ok"}
	if err := scheduler.Save(s); err != nil {
		t.Fatal(err)
	}

	// 更新 prompt / 切换为 cron；created_at 与 last_run 应保留。
	if _, err := tool.Execute(scheduleCtx(ws), nil, `{"name":"task","prompt":"v2","cron":"0 9 * * *"}`); err != nil {
		t.Fatal(err)
	}
	loaded, err := scheduler.Load("task")
	if err != nil || loaded == nil {
		t.Fatalf("Load = (%v, %v)", loaded, err)
	}
	if loaded.Prompt != "v2" {
		t.Fatalf("prompt = %q, want v2", loaded.Prompt)
	}
	if loaded.Cron != "0 9 * * *" {
		t.Fatalf("cron = %q, want updated", loaded.Cron)
	}
	if loaded.LastRun == nil || !loaded.LastRun.Success || loaded.LastRun.Summary != "ok" {
		t.Fatalf("last_run not preserved: %+v", loaded.LastRun)
	}
}

func TestScheduleTaskRejectsBadInput(t *testing.T) {
	ws := scheduleTestContext(t)
	tool := &scheduleTaskTool{}

	// 无产出区 → 报错。
	if _, err := tool.Execute(context.Background(), nil, `{"name":"x","prompt":"p","interval":"1h"}`); err == nil {
		t.Fatal("expected error when no output cwd in context")
	}
	// 名称为空 id → 报错。
	if _, err := tool.Execute(scheduleCtx(ws), nil, `{"name":"!!!","prompt":"p","interval":"1h"}`); err == nil {
		t.Fatal("expected error for name without letters/digits")
	}
	// 同时给 cron+interval → 报错。
	if _, err := tool.Execute(scheduleCtx(ws), nil, `{"name":"x","prompt":"p","cron":"0 9 * * *","interval":"1h"}`); err == nil {
		t.Fatal("expected error for cron+interval")
	}
	// 坏 cron → 报错。
	if _, err := tool.Execute(scheduleCtx(ws), nil, `{"name":"x","prompt":"p","cron":"bad"}`); err == nil {
		t.Fatal("expected error for bad cron")
	}
	// 目录不存在 → 不应创建任何排程。
	if n, _ := scheduler.List(); len(n) != 0 {
		t.Fatalf("no schedule should be created, got %d", len(n))
	}
}

func TestScheduleListAndRemove(t *testing.T) {
	ws := scheduleTestContext(t)
	create := &scheduleTaskTool{}
	if _, err := create.Execute(scheduleCtx(ws), nil, `{"name":"task-a","prompt":"a","interval":"1h"}`); err != nil {
		t.Fatal(err)
	}

	list := &scheduleListTool{}
	out, err := list.Execute(context.Background(), nil, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "task-a") || !strings.Contains(out, "every 1h") {
		t.Fatalf("list should contain task with interval:\n%s", out)
	}

	rm := &scheduleRemoveTool{}
	if _, err := rm.Execute(context.Background(), nil, `{"id":"task-a"}`); err != nil {
		t.Fatal(err)
	}
	if s, _ := scheduler.Load("task-a"); s != nil {
		t.Fatalf("task-a should be removed, got %+v", s)
	}
	// 删除不存在的 → 不报错。
	if _, err := rm.Execute(context.Background(), nil, `{"id":"nope"}`); err != nil {
		t.Fatalf("remove missing should not error: %v", err)
	}
	// 空 id → 报错。
	if _, err := rm.Execute(context.Background(), nil, `{"id":""}`); err == nil {
		t.Fatal("expected error for empty id")
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestScheduleTaskProjectLevelStorage(t *testing.T) {
	ws := scheduleTestContext(t)
	// git/marked 工作区 → 排程落项目级 <root>/.cata/schedules/。
	ws.Kind = brain.KindGit
	tool := &scheduleTaskTool{}
	if _, err := tool.Execute(scheduleCtx(ws), nil, `{"name":"proj-task","prompt":"项目级任务","interval":"1h"}`); err != nil {
		t.Fatal(err)
	}
	got := filepath.Join(ws.RootPath, brain.ProjectCataDir, "schedules", "proj-task.json")
	if !fileExists(got) {
		t.Fatalf("project-level schedule file missing at %s", got)
	}
	// 机器级不应存在。
	if fileExists(filepath.Join(config.CataHome(), "schedules", "proj-task.json")) {
		t.Fatal("project schedule should not be stored at machine level")
	}
	s, err := scheduler.Load("proj-task")
	if err != nil || s != nil {
		t.Fatalf("machine Load should be nil, got %v %v", s, err)
	}
	proj, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(proj), `"project": "`+ws.RootPath+`"`) {
		t.Fatalf("schedule json should carry project root:\n%s", proj)
	}

	// 更新时保留项目级存储位置。
	if _, err := tool.Execute(scheduleCtx(ws), nil, `{"name":"proj-task","prompt":"v2","cron":"0 9 * * *"}`); err != nil {
		t.Fatal(err)
	}
	proj2, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(proj2), `"project": "`+ws.RootPath+`"`) || !strings.Contains(string(proj2), "v2") {
		t.Fatalf("update should keep project storage and update prompt:\n%s", proj2)
	}
	// Remove 也应能删除项目级（Find 跨机器/项目搜索，需工作区已注册）。
	if _, err := brain.ResolveWorkspace(ws.RootPath); err != nil {
		t.Fatal(err)
	}
	if _, err := (&scheduleRemoveTool{}).Execute(context.Background(), nil, `{"id":"proj-task"}`); err != nil {
		t.Fatal(err)
	}
	if fileExists(got) {
		t.Fatal("project schedule should be removed")
	}
}

func TestScheduleTaskEnsuresDaemonWhenEnabled(t *testing.T) {
	ws := scheduleTestContext(t)
	called := 0
	old := ensureSchedulerDaemon
	ensureSchedulerDaemon = func() error { called++; return nil }
	defer func() { ensureSchedulerDaemon = old }()

	tool := &scheduleTaskTool{}
	out, err := tool.Execute(scheduleCtx(ws), nil, `{"name":"daemon-check","prompt":"p","interval":"1h"}`)
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatalf("ensureSchedulerDaemon called %d times, want 1 (enabled default true)", called)
	}
	if !strings.Contains(out, "调度守护已在后台运行") {
		t.Fatalf("output should mention daemon running: %s", out)
	}

	// 显式 enabled=false 不拉起守护。
	if _, err := tool.Execute(scheduleCtx(ws), nil, `{"name":"daemon-off","prompt":"p","interval":"1h","enabled":false}`); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatalf("disabled task should not ensure daemon, called=%d", called)
	}
}

func TestScheduleTaskSkipsDaemonWhenSchedulesDisabled(t *testing.T) {
	ws := scheduleTestContext(t)
	oldCfg := config.Config
	cfg := &config.AppConfig{}
	cfg.Schedules.Enabled = boolPtr(false)
	config.Config = cfg
	t.Cleanup(func() { config.Config = oldCfg })

	called := 0
	old := ensureSchedulerDaemon
	ensureSchedulerDaemon = func() error { called++; return nil }
	defer func() { ensureSchedulerDaemon = old }()

	tool := &scheduleTaskTool{}
	out, err := tool.Execute(scheduleCtx(ws), nil, `{"name":"disabled-cfg","prompt":"p","interval":"1h"}`)
	if err != nil {
		t.Fatal(err)
	}
	if called != 0 {
		t.Fatalf("daemon should not be ensured when schedules disabled, called=%d", called)
	}
	if !strings.Contains(out, "schedules.enabled=false") {
		t.Fatalf("output should mention schedules disabled: %s", out)
	}
}

func boolPtr(b bool) *bool { return &b }
