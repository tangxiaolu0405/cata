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
	out, err := list.Execute(scheduleCtx(ws), nil, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "task-a") || !strings.Contains(out, "every 1h") {
		t.Fatalf("list should contain task with interval:\n%s", out)
	}

	rm := &scheduleRemoveTool{}
	if _, err := rm.Execute(scheduleCtx(ws), nil, `{"id":"task-a"}`); err != nil {
		t.Fatal(err)
	}
	if s, _ := scheduler.Load("task-a"); s != nil {
		t.Fatalf("task-a should be removed, got %+v", s)
	}
	// 删除不存在的 → 报错。
	if _, err := rm.Execute(scheduleCtx(ws), nil, `{"id":"nope"}`); err == nil {
		t.Fatal("remove missing should error")
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
	if _, err := (&scheduleRemoveTool{}).Execute(scheduleCtx(ws), nil, `{"id":"proj-task"}`); err != nil {
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

// scheduleCrossWorkspaceContext 构造两个已注册的 git 工作区（项目级排程依赖 registry 发现）。
func scheduleCrossWorkspaceContext(t *testing.T) (*brain.Workspace, *brain.Workspace) {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)
	rootA := t.TempDir()
	rootB := t.TempDir()
	for _, root := range []string{rootA, rootB} {
		if _, err := brain.ResolveWorkspace(root); err != nil {
			t.Fatalf("ResolveWorkspace(%s): %v", root, err)
		}
	}
	wsA := &brain.Workspace{ID: "ws-a", RootPath: rootA, Kind: brain.KindGit, ActiveMode: brain.ModeDefaultID}
	wsB := &brain.Workspace{ID: "ws-b", RootPath: rootB, Kind: brain.KindGit, ActiveMode: brain.ModeDefaultID}
	for _, ws := range []*brain.Workspace{wsA, wsB} {
		if err := os.MkdirAll(ws.ModeDir(brain.ModeDefaultID), 0755); err != nil {
			t.Fatal(err)
		}
	}
	old := ensureSchedulerDaemon
	ensureSchedulerDaemon = func() error { return nil }
	t.Cleanup(func() { ensureSchedulerDaemon = old })
	return wsA, wsB
}

func TestScheduleListScopedToWorkspace(t *testing.T) {
	wsA, wsB := scheduleCrossWorkspaceContext(t)
	if _, err := (&scheduleTaskTool{}).Execute(scheduleCtx(wsA), nil, `{"name":"a-task","prompt":"a","interval":"1h"}`); err != nil {
		t.Fatal(err)
	}
	list := &scheduleListTool{}
	outA, err := list.Execute(scheduleCtx(wsA), nil, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(outA, "a-task") {
		t.Fatalf("workspace A should see its own task:\n%s", outA)
	}
	outB, err := list.Execute(scheduleCtx(wsB), nil, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(outB, "a-task") {
		t.Fatalf("workspace B must not see A's task (no cross-workspace):\n%s", outB)
	}
}

func TestScheduleMachineLevelScopedToCreatingWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)
	wsA := &brain.Workspace{ID: "ws-a", RootPath: t.TempDir(), ActiveMode: brain.ModeDefaultID}
	wsB := &brain.Workspace{ID: "ws-b", RootPath: t.TempDir(), ActiveMode: brain.ModeDefaultID}
	for _, ws := range []*brain.Workspace{wsA, wsB} {
		if err := os.MkdirAll(ws.ModeDir(brain.ModeDefaultID), 0755); err != nil {
			t.Fatal(err)
		}
	}
	old := ensureSchedulerDaemon
	ensureSchedulerDaemon = func() error { return nil }
	t.Cleanup(func() { ensureSchedulerDaemon = old })

	if _, err := (&scheduleTaskTool{}).Execute(scheduleCtx(wsA), nil, `{"name":"m-task","prompt":"p","interval":"1h"}`); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config.CataHome(), "schedules", "m-task.json")
	if !fileExists(path) {
		t.Fatalf("machine-level schedule file missing at %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"ws_id": "ws-a"`) {
		t.Fatalf("machine-level task should carry ws_id ws-a:\n%s", data)
	}

	list := &scheduleListTool{}
	outA, err := list.Execute(scheduleCtx(wsA), nil, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(outA, "m-task") {
		t.Fatalf("workspace A should see its own machine-level task:\n%s", outA)
	}
	outB, err := list.Execute(scheduleCtx(wsB), nil, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(outB, "m-task") {
		t.Fatalf("workspace B must not see A's machine-level task:\n%s", outB)
	}
}

func TestScheduleCancel(t *testing.T) {
	ws := scheduleTestContext(t)
	if _, err := (&scheduleTaskTool{}).Execute(scheduleCtx(ws), nil, `{"name":"cancel-me","prompt":"p","interval":"1h"}`); err != nil {
		t.Fatal(err)
	}
	cancel := &scheduleCancelTool{}
	out, err := cancel.Execute(scheduleCtx(ws), nil, `{"id":"cancel-me"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "cancel-me") || !strings.Contains(out, "enabled=false") {
		t.Fatalf("cancel output: %s", out)
	}
	s, err := scheduler.Load("cancel-me")
	if err != nil || s == nil {
		t.Fatalf("Load = (%v, %v)", s, err)
	}
	if s.Enabled {
		t.Fatal("task should be disabled after cancel")
	}
	if s.NextRun != "" {
		t.Fatalf("next_run should be cleared after cancel, got %q", s.NextRun)
	}

	// 再次取消 → 幂等。
	if _, err := cancel.Execute(scheduleCtx(ws), nil, `{"id":"cancel-me"}`); err != nil {
		t.Fatal(err)
	}

	// 跨工作区取消 → 拒绝。
	wsB := &brain.Workspace{ID: "ws-b", RootPath: t.TempDir(), ActiveMode: brain.ModeDefaultID}
	if err := os.MkdirAll(wsB.ModeDir(brain.ModeDefaultID), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := cancel.Execute(scheduleCtx(wsB), nil, `{"id":"cancel-me"}`); err == nil {
		t.Fatal("cross-workspace cancel should be refused")
	}

	// 重新启用：schedule_task 同名 + enabled=true → next_run 重算。
	if _, err := (&scheduleTaskTool{}).Execute(scheduleCtx(ws), nil, `{"name":"cancel-me","prompt":"p","interval":"1h","enabled":true}`); err != nil {
		t.Fatal(err)
	}
	s2, err := scheduler.Load("cancel-me")
	if err != nil || s2 == nil {
		t.Fatalf("Load after re-enable = (%v, %v)", s2, err)
	}
	if !s2.Enabled {
		t.Fatal("re-enable should set enabled=true")
	}
	if s2.NextRun == "" {
		t.Fatal("re-enabled task should have next_run")
	}
}

func TestScheduleRemoveScopedToWorkspace(t *testing.T) {
	wsA, wsB := scheduleCrossWorkspaceContext(t)
	if _, err := (&scheduleTaskTool{}).Execute(scheduleCtx(wsA), nil, `{"name":"a-rm","prompt":"p","interval":"1h"}`); err != nil {
		t.Fatal(err)
	}
	rm := &scheduleRemoveTool{}
	// B 删除 A 的任务 → 拒绝（不泄露存在性）。
	if _, err := rm.Execute(scheduleCtx(wsB), nil, `{"id":"a-rm"}`); err == nil {
		t.Fatal("cross-workspace remove should be refused")
	}
	// A 删除自己的 → 成功。
	if _, err := rm.Execute(scheduleCtx(wsA), nil, `{"id":"a-rm"}`); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(wsA.RootPath, brain.ProjectCataDir, "schedules", "a-rm.json")) {
		t.Fatal("a-rm project schedule file should be removed")
	}
	// 已删除 → 再删报错。
	if _, err := rm.Execute(scheduleCtx(wsA), nil, `{"id":"a-rm"}`); err == nil {
		t.Fatal("removing missing should error")
	}
}
