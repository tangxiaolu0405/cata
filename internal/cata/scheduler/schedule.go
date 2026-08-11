// Package scheduler 提供轻量定时任务：排程定义（机器级 ~/.cata/schedules/<id>.json 或
// 项目级 <project>/.cata/schedules/<id>.json）、5 字段 cron / 简单间隔解析与到点触发引擎。
// 调度框架（cata schedule 守护进程）用它发现环境中的任务并在到点时以真实客户端自发起执行。
// 纯逻辑包，不依赖 server。
package scheduler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"cata/internal/cata/brain"
	"cata/internal/cata/clock"
	"cata/internal/cata/config"
)

// RunInfo 最近一次运行结果（落盘在排程 JSON 中，供 schedule_list 展示）。
type RunInfo struct {
	At      string `json:"at"`
	Success bool   `json:"success"`
	Summary string `json:"summary,omitempty"`
	Report  string `json:"report,omitempty"`
}

// Schedule 一条定时任务定义。
// Cron 与 Interval 二选一：Cron 为 5 字段 cron；Interval 为 time.Duration 字符串（如 "24h"、"30m"）。
type Schedule struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Prompt    string `json:"prompt"`
	Cron      string `json:"cron,omitempty"`
	Interval  string `json:"interval,omitempty"`
	Cwd       string `json:"cwd"`
	WSID      string `json:"ws_id,omitempty"`
	ModeID    string `json:"mode_id,omitempty"`
	AllowExec bool   `json:"allow_exec,omitempty"`
	Enabled   bool   `json:"enabled"`
	OutputDir string `json:"output_dir,omitempty"`
	// Project 项目根（非空 = 项目级排程，存 <project>/.cata/schedules/；空 = 机器级 ~/.cata/schedules/）。
	Project   string   `json:"project,omitempty"`
	CreatedAt string   `json:"created_at"`
	LastRun   *RunInfo `json:"last_run,omitempty"`
	NextRun   string   `json:"next_run,omitempty"`
}

// ProjectSchedulesDir 项目级排程目录（<project>/.cata/schedules）。
func ProjectSchedulesDir(projectRoot string) string {
	return filepath.Join(projectRoot, brain.ProjectCataDir, "schedules")
}

// DirFor 某条排程的存储目录（项目级或机器级）。
func DirFor(s *Schedule) string {
	if s != nil && strings.TrimSpace(s.Project) != "" {
		return ProjectSchedulesDir(s.Project)
	}
	return Dir()
}

// PathFor 某条排程的 JSON 路径。
func PathFor(s *Schedule) string {
	return filepath.Join(DirFor(s), s.ID+".json")
}

// RunsDirFor 某条排程的运行审计目录（<存储目录>/runs/<id>/）。
func RunsDirFor(s *Schedule) string {
	return filepath.Join(DirFor(s), "runs", s.ID)
}

// Dir 排程存储目录（CATA_HOME/schedules）。
func Dir() string {
	return filepath.Join(config.CataHome(), "schedules")
}

// RunsDir 运行审计目录（CATA_HOME/schedules/runs/<id>/）。
func RunsDir(id string) string {
	return filepath.Join(Dir(), "runs", id)
}

// Path 某条排程的 JSON 路径。
func Path(id string) string {
	return filepath.Join(Dir(), id+".json")
}

// IDFromName 把名称转成稳定 id（保留字母/数字含 CJK，其余转 '-'、去首尾 '-'）。
func IDFromName(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// Validate 校验排程必填项与排程语法。
func (s *Schedule) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("schedule: name required")
	}
	if strings.TrimSpace(s.Prompt) == "" {
		return fmt.Errorf("schedule: prompt required")
	}
	if strings.TrimSpace(s.Cwd) == "" {
		return fmt.Errorf("schedule: cwd required")
	}
	hasCron := strings.TrimSpace(s.Cron) != ""
	hasInterval := strings.TrimSpace(s.Interval) != ""
	if hasCron == hasInterval {
		return fmt.Errorf("schedule: exactly one of cron or interval required")
	}
	if hasCron {
		if _, err := ParseCron(s.Cron); err != nil {
			return fmt.Errorf("schedule: bad cron %q: %w", s.Cron, err)
		}
	}
	if hasInterval {
		d, err := time.ParseDuration(strings.TrimSpace(s.Interval))
		if err != nil || d < time.Minute {
			return fmt.Errorf("schedule: bad interval %q (min 1m)", s.Interval)
		}
	}
	return nil
}

// NextFire 计算下一次触发时间（在 after 之后）。
func (s *Schedule) NextFire(after time.Time) (time.Time, error) {
	if c := strings.TrimSpace(s.Cron); c != "" {
		expr, err := ParseCron(c)
		if err != nil {
			return time.Time{}, err
		}
		return expr.Next(after), nil
	}
	d, err := time.ParseDuration(strings.TrimSpace(s.Interval))
	if err != nil {
		return time.Time{}, err
	}
	return after.Add(d), nil
}

// Load 读取单条排程；不存在返回 (nil, nil)。
func Load(id string) (*Schedule, error) {
	data, err := os.ReadFile(Path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var s Schedule
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Save 原子写回排程（并确保目录存在）。
func Save(s *Schedule) error {
	if s == nil {
		return fmt.Errorf("schedule: nil")
	}
	if s.ID == "" {
		s.ID = IDFromName(s.Name)
	}
	if err := s.Validate(); err != nil {
		return err
	}
	if s.CreatedAt == "" {
		s.CreatedAt = clock.RFC3339()
	}
	if s.NextRun == "" {
		if next, err := s.NextFire(clock.Now()); err == nil {
			s.NextRun = clock.FormatTime(next, time.RFC3339)
		}
	}
	if err := os.MkdirAll(DirFor(s), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := PathFor(s) + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, PathFor(s))
}

// Remove 删除一条排程（按 id 搜索机器级与全部项目级位置）；不存在返回 nil。
func Remove(id string) error {
	found, _, err := Find(id)
	if err != nil {
		return err
	}
	if found == nil {
		return nil
	}
	err = os.Remove(PathFor(found))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// listDir 读取单个排程目录下的全部排程。
func listDir(dir string) ([]*Schedule, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*Schedule
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var s Schedule
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		out = append(out, &s)
	}
	return out, nil
}

// List 列出机器级排程（按 id 排序；旧接口，schedule_list 请用 ListAll）。
func List() ([]*Schedule, error) {
	return listDir(Dir())
}

// workspaceProjectRoots 返回全部已注册工作区的项目根（环境发现：项目级排程来源）。
func workspaceProjectRoots() []string {
	wss, err := brain.ListWorkspaces()
	if err != nil {
		return nil
	}
	var roots []string
	for _, ws := range wss {
		if strings.TrimSpace(ws.RootPath) == "" {
			continue
		}
		roots = append(roots, ws.RootPath)
	}
	return roots
}

// ListAll 列出环境中的全部排程：机器级 + 每个已注册工作区的项目级（发现「那个环境有啥任务」）。
// 同 id 出现在多个位置时，机器级优先（每任务一个定义，去重保留第一个）。
func ListAll() ([]*Schedule, error) {
	var out []*Schedule
	seen := map[string]bool{}
	add := func(list []*Schedule, err error) error {
		if err != nil {
			return err
		}
		for _, s := range list {
			if seen[s.ID] {
				continue
			}
			seen[s.ID] = true
			out = append(out, s)
		}
		return nil
	}
	if err := add(listDir(Dir())); err != nil {
		return nil, err
	}
	for _, root := range workspaceProjectRoots() {
		if err := add(listDir(ProjectSchedulesDir(root))); err != nil {
			continue // 单个项目目录读取失败不阻断整体发现
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Find 按 id 搜索排程（机器级优先，再项目级），返回排程与其存储目录；未找到返回 (nil, "", nil)。
func Find(id string) (*Schedule, string, error) {
	if strings.TrimSpace(id) == "" {
		return nil, "", nil
	}
	if s, err := Load(id); err != nil {
		return nil, "", err
	} else if s != nil {
		return s, Dir(), nil
	}
	for _, root := range workspaceProjectRoots() {
		dir := ProjectSchedulesDir(root)
		data, err := os.ReadFile(filepath.Join(dir, id+".json"))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, "", err
		}
		var s Schedule
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		return &s, dir, nil
	}
	return nil, "", nil
}
