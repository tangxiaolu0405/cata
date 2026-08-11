package scheduler

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"cata/internal/cata/brain"
	"cata/internal/cata/clock"
	"cata/internal/cata/config"
)

// setupSchedulerHome 用临时 CATA_HOME 隔离排程存储。
func setupSchedulerHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)
	return home
}

func TestIDFromName(t *testing.T) {
	cases := map[string]string{
		"Daily Product":   "daily-product",
		"  My Task 123  ": "my-task-123",
		"每日选品":            "每日选品",
		"跨境电商-选品日报":       "跨境电商-选品日报",
		"!!!":             "",
		"a!b@c#d":         "a-b-c-d",
	}
	for in, want := range cases {
		if got := IDFromName(in); got != want {
			t.Errorf("IDFromName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScheduleValidate(t *testing.T) {
	base := func() *Schedule {
		return &Schedule{Name: "task", Prompt: "do something", Cwd: "/tmp", Interval: "1h"}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("valid schedule rejected: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*Schedule)
	}{
		{"empty name", func(s *Schedule) { s.Name = "  " }},
		{"empty prompt", func(s *Schedule) { s.Prompt = "" }},
		{"empty cwd", func(s *Schedule) { s.Cwd = "" }},
		{"both cron and interval", func(s *Schedule) { s.Cron = "0 9 * * *" }},
		{"neither cron nor interval", func(s *Schedule) { s.Interval = "" }},
		{"bad cron", func(s *Schedule) { s.Interval = ""; s.Cron = "not a cron" }},
		{"bad interval", func(s *Schedule) { s.Interval = "5s" }},
		{"interval below 1m", func(s *Schedule) { s.Interval = "30s" }},
	}
	for _, c := range cases {
		s := base()
		c.mut(s)
		if err := s.Validate(); err == nil {
			t.Errorf("%s: expected error, got nil", c.name)
		}
	}
}

func TestScheduleNextFire(t *testing.T) {
	loc := clock.Location()
	after := time.Date(2026, 8, 11, 10, 0, 0, 0, loc)

	s := &Schedule{Interval: "90m"}
	next, err := s.NextFire(after)
	if err != nil {
		t.Fatal(err)
	}
	if want := after.Add(90 * time.Minute); !next.Equal(want) {
		t.Fatalf("interval NextFire = %v, want %v", next, want)
	}

	c := &Schedule{Cron: "0 9 * * *"}
	next, err = c.NextFire(after)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 8, 12, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("cron NextFire = %v, want %v", next, want)
	}
}

func TestScheduleSaveLoadListRemove(t *testing.T) {
	home := setupSchedulerHome(t)

	s := &Schedule{
		Name:     "Daily product research",
		Prompt:   "研究今天的热门选品",
		Cwd:      "/tmp/work",
		Interval: "24h",
		Enabled:  true,
	}
	if err := Save(s); err != nil {
		t.Fatal(err)
	}
	if s.ID != "daily-product-research" {
		t.Fatalf("Save assigned id %q, want daily-product-research", s.ID)
	}
	if s.NextRun == "" {
		t.Fatal("Save should compute next_run")
	}
	if got := filepath.Join(home, "schedules", s.ID+".json"); Path(s.ID) != got {
		t.Fatalf("Path(%q) = %q, want %q", s.ID, Path(s.ID), got)
	}

	loaded, err := Load(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil {
		t.Fatal("Load returned nil for existing schedule")
	}
	if loaded.Name != s.Name || loaded.Prompt != s.Prompt || loaded.Cwd != s.Cwd {
		t.Fatalf("loaded mismatch: %+v", loaded)
	}

	all, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].ID != s.ID {
		t.Fatalf("List = %d items, want 1 with id %s", len(all), s.ID)
	}

	// 不存在 → nil, nil
	missing, err := Load("nope")
	if err != nil || missing != nil {
		t.Fatalf("Load(missing) = (%v, %v), want (nil, nil)", missing, err)
	}

	if err := Remove(s.ID); err != nil {
		t.Fatal(err)
	}
	if loaded, _ := Load(s.ID); loaded != nil {
		t.Fatalf("schedule should be removed, still loaded: %+v", loaded)
	}
	if err := Remove(s.ID); err != nil {
		t.Fatalf("Remove missing should be nil, got %v", err)
	}
}

func TestScheduleProjectLevelDiscovery(t *testing.T) {
	setupSchedulerHome(t)
	root := t.TempDir()
	// 注册工作区：项目级排程发现（ListAll/Find）依赖工作区 registry。
	if _, err := brain.ResolveWorkspace(root); err != nil {
		t.Fatal(err)
	}

	s := &Schedule{
		Name:     "project task",
		Prompt:   "project prompt",
		Cwd:      root,
		Interval: "1h",
		Enabled:  true,
		Project:  root,
	}
	if err := Save(s); err != nil {
		t.Fatal(err)
	}
	// 落盘在项目 .cata/schedules/<id>.json，而非机器级。
	if _, err := os.Stat(filepath.Join(root, brain.ProjectCataDir, "schedules", s.ID+".json")); err != nil {
		t.Fatalf("project schedule file missing: %v", err)
	}
	if got, err := Load(s.ID); err != nil || got != nil {
		t.Fatalf("Load (machine) for project schedule = (%v, %v), want (nil, nil)", got, err)
	}

	// Find/ListAll 能发现项目级。
	found, dir, err := Find(s.ID)
	if err != nil || found == nil {
		t.Fatalf("Find = (%v, %v, %v), want schedule", found, dir, err)
	}
	if dir != ProjectSchedulesDir(root) {
		t.Fatalf("Find dir = %q, want %q", dir, ProjectSchedulesDir(root))
	}
	all, err := ListAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].ID != s.ID || all[0].Project != root {
		t.Fatalf("ListAll = %+v, want 1 project schedule", all)
	}

	// 机器级优先：同 id 写机器级后，Find/ListAll 返回机器级那条。
	m := &Schedule{ID: s.ID, Name: "project task", Prompt: "machine prompt", Cwd: "/tmp", Interval: "1h", Enabled: true}
	if err := Save(m); err != nil {
		t.Fatal(err)
	}
	found2, dir2, err := Find(s.ID)
	if err != nil || found2 == nil || dir2 != Dir() {
		t.Fatalf("machine-level should win: dir=%q found=%+v err=%v", dir2, found2, err)
	}
	all2, _ := ListAll()
	if len(all2) != 1 || all2[0].Prompt != "machine prompt" {
		t.Fatalf("ListAll should dedupe with machine priority: %+v", all2)
	}

	// Remove 按 id 先删机器级；项目级仍在。
	if err := Remove(s.ID); err != nil {
		t.Fatal(err)
	}
	if found3, _, err := Find(s.ID); err != nil || found3 == nil {
		t.Fatalf("after removing machine-level, Find should return project-level: %v %v", found3, err)
	}
}

// TestScheduleRunKey 同 id 不同项目应视为不同任务（防重入 key）。
func TestScheduleRunKey(t *testing.T) {
	a := &Schedule{ID: "x", Project: ""}
	b := &Schedule{ID: "x", Project: "/proj"}
	if runKey(a) == runKey(b) {
		t.Fatal("runKey should distinguish same id across machine/project")
	}
	if runKey(a) != runKey(&Schedule{ID: "x", Project: ""}) {
		t.Fatal("runKey should be stable for same id+project")
	}
}
