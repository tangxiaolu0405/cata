package brain

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cata/internal/cata/clock"
)

const (
	DirTasks          = "tasks"
	FileTaskCurrent   = "current.json"
	TaskStatusIdle    = "idle"
	TaskStatusRunning = "running"
	TaskStatusFailed  = "failed"
	TaskStatusDone    = "done"
	TaskStatusPaused  = "paused"
)

// TaskState 可恢复任务状态（home 脑子格 tasks/current.json）。
// Acceptance 与终止限额均由 LLM/用户按任务指定；系统不套统一业务标准。
type TaskState struct {
	ID                      string   `json:"id"`
	Status                  string   `json:"status"`
	Goal                    string   `json:"goal,omitempty"`
	Acceptance              []string `json:"acceptance,omitempty"`
	Steps                   []string `json:"steps,omitempty"`
	// 终止条件（按任务）；0 = 未声明，该维度不熔断（轮次仍受全局 hard ceiling 约束）。
	MaxToolRounds          int `json:"max_tool_rounds,omitempty"`
	MaxConsecutiveFailures int `json:"max_consecutive_failures,omitempty"`
	MaxStaleRounds         int `json:"max_stale_rounds,omitempty"`
	Round                   int      `json:"round"`
	ConsecutiveFailures     int      `json:"consecutive_failures"`
	StaleRounds             int      `json:"stale_rounds"`
	LastTool                string   `json:"last_tool,omitempty"`
	LastError               string   `json:"last_error,omitempty"`
	LastProgressFingerprint string   `json:"last_progress_fingerprint,omitempty"`
	FailCode                string   `json:"fail_code,omitempty"`
	FailReason              string   `json:"fail_reason,omitempty"`
	OutputCwd               string   `json:"output_cwd,omitempty"`
	FocusPath               string   `json:"focus_path,omitempty"`
	CreatedAt               string   `json:"created_at"`
	UpdatedAt               string   `json:"updated_at"`
}

// TaskContract 声明/更新任务契约的入参。
type TaskContract struct {
	Goal                   string
	Acceptance             []string
	Steps                  []string
	MaxToolRounds          *int
	MaxConsecutiveFailures *int
	MaxStaleRounds         *int
	SetAcceptance          bool
	SetSteps               bool
}

// MarkTaskFailed 预算/熔断后写入失败态（可「继续」恢复）。
func MarkTaskFailed(w *Workspace, st *TaskState, code, reason string, round, consec, stale int, lastTool, fp string) error {
	if st == nil {
		return fmt.Errorf("nil task")
	}
	st.Status = TaskStatusFailed
	st.FailCode = code
	st.FailReason = reason
	st.LastError = reason
	st.Round = round
	st.ConsecutiveFailures = consec
	st.StaleRounds = stale
	st.LastTool = lastTool
	st.LastProgressFingerprint = fp
	return SaveCurrentTask(w, st)
}

// MarkTaskDone 模型正常收工时标记完成。
func MarkTaskDone(w *Workspace, st *TaskState, round int) error {
	if st == nil {
		return nil
	}
	st.Status = TaskStatusDone
	st.Round = round
	st.FailCode = ""
	st.FailReason = ""
	st.LastError = ""
	return SaveCurrentTask(w, st)
}

// UpdateTaskContract 由 declare_task 写入目标/验收/步骤/终止限额（均按任务指定）。
func UpdateTaskContract(w *Workspace, c TaskContract) (*TaskState, error) {
	st, err := LoadCurrentTask(w)
	if err != nil {
		return nil, err
	}
	if st == nil {
		st = &TaskState{
			ID:        newTaskID(),
			Status:    TaskStatusRunning,
			CreatedAt: clock.RFC3339(),
		}
	}
	if g := strings.TrimSpace(c.Goal); g != "" {
		st.Goal = g
	}
	if c.SetAcceptance {
		st.Acceptance = c.Acceptance
	}
	if c.SetSteps {
		st.Steps = c.Steps
	}
	if c.MaxToolRounds != nil {
		st.MaxToolRounds = *c.MaxToolRounds
	}
	if c.MaxConsecutiveFailures != nil {
		st.MaxConsecutiveFailures = *c.MaxConsecutiveFailures
	}
	if c.MaxStaleRounds != nil {
		st.MaxStaleRounds = *c.MaxStaleRounds
	}
	if st.Status == TaskStatusDone || st.Status == TaskStatusIdle {
		st.Status = TaskStatusRunning
	}
	st.FocusPath = w.RootPath
	if err := SaveCurrentTask(w, st); err != nil {
		return nil, err
	}
	return st, nil
}

// TaskResumeProgressMessage 给客户端的续跑提示。
func TaskResumeProgressMessage(st *TaskState, resumed bool) string {
	if st == nil {
		return ""
	}
	if !resumed {
		return fmt.Sprintf("task %s started (status=%s)", st.ID, st.Status)
	}
	msg := fmt.Sprintf("resuming task %s (status=%s) goal=%q", st.ID, st.Status, truncateRunes(st.Goal, 120))
	if st.FailCode != "" {
		msg += fmt.Sprintf(" prior_fail=%s", st.FailCode)
	}
	if len(st.Acceptance) > 0 {
		msg += fmt.Sprintf(" acceptance=%d criteria", len(st.Acceptance))
	}
	return msg
}

// TaskDir home 格 tasks 目录。
func (w *Workspace) TaskDir() string {
	if w == nil {
		return filepath.Join(CataHome(), DirBrain, DirWorkspaces, "default", DirTasks)
	}
	return filepath.Join(w.Dir(), DirTasks)
}

// CurrentTaskPath 当前任务状态文件。
func (w *Workspace) CurrentTaskPath() string {
	return filepath.Join(w.TaskDir(), FileTaskCurrent)
}

// LoadCurrentTask 读取当前任务；不存在返回 nil。
func LoadCurrentTask(w *Workspace) (*TaskState, error) {
	if w == nil {
		return nil, fmt.Errorf("no workspace")
	}
	data, err := os.ReadFile(w.CurrentTaskPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var st TaskState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// SaveCurrentTask 原子写回当前任务。
func SaveCurrentTask(w *Workspace, st *TaskState) error {
	if w == nil || st == nil {
		return fmt.Errorf("workspace/task required")
	}
	st.UpdatedAt = clock.RFC3339()
	if st.CreatedAt == "" {
		st.CreatedAt = st.UpdatedAt
	}
	if err := os.MkdirAll(w.TaskDir(), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := w.CurrentTaskPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, w.CurrentTaskPath())
}

// BeginOrResumeTask 新用户消息时开启或续跑任务。
func BeginOrResumeTask(w *Workspace, userText, outputCwd string) (*TaskState, bool, error) {
	prev, err := LoadCurrentTask(w)
	if err != nil {
		return nil, false, err
	}
	resuming := prev != nil && (prev.Status == TaskStatusRunning || prev.Status == TaskStatusFailed || prev.Status == TaskStatusPaused)
	if resuming && prev.Status == TaskStatusRunning {
		prev.Goal = firstNonEmpty(strings.TrimSpace(userText), prev.Goal)
		prev.OutputCwd = firstNonEmpty(outputCwd, prev.OutputCwd)
		prev.FocusPath = w.RootPath
		if err := SaveCurrentTask(w, prev); err != nil {
			return nil, true, err
		}
		return prev, true, nil
	}
	if resuming && (prev.Status == TaskStatusFailed || prev.Status == TaskStatusPaused) {
		if isContinueUtterance(userText) {
			prev.Status = TaskStatusRunning
			prev.FailCode = ""
			prev.FailReason = ""
			prev.LastError = ""
			prev.ConsecutiveFailures = 0
			prev.StaleRounds = 0
			if err := SaveCurrentTask(w, prev); err != nil {
				return nil, true, err
			}
			return prev, true, nil
		}
	}
	st := &TaskState{
		ID:        newTaskID(),
		Status:    TaskStatusRunning,
		Goal:      strings.TrimSpace(userText),
		OutputCwd: outputCwd,
		FocusPath: w.RootPath,
		CreatedAt: clock.RFC3339(),
	}
	if err := SaveCurrentTask(w, st); err != nil {
		return nil, false, err
	}
	return st, false, nil
}

// ClearCurrentTask chat_reset 时归档并清空。
func ClearCurrentTask(w *Workspace) error {
	st, err := LoadCurrentTask(w)
	if err != nil || st == nil {
		return err
	}
	st.Status = TaskStatusPaused
	_ = archiveTask(w, st)
	return os.Remove(w.CurrentTaskPath())
}

func archiveTask(w *Workspace, st *TaskState) error {
	dir := filepath.Join(w.TaskDir(), "archive")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	name := st.ID + ".json"
	if name == ".json" {
		name = fmt.Sprintf("task-%d.json", time.Now().Unix())
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), append(data, '\n'), 0644)
}

func isContinueUtterance(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	switch s {
	case "继续", "continue", "接着", "接着做", "go on", "resume":
		return true
	}
	return strings.HasPrefix(s, "继续") || strings.HasPrefix(s, "continue")
}

func newTaskID() string {
	now := clock.Now()
	return fmt.Sprintf("t%s%03d", now.Format("20060102-150405"), now.Nanosecond()/1e6)
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
