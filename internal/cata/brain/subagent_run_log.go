package brain

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"cata/internal/cata/clock"
)

const (
	subagentCSVHeader = "session_id,delegate_index,started_at,finished_at,id,workspace,output_cwd,model,status,rounds,tools,task,context,summary"
	maxSubagentField  = 4000
	maxSubagentName   = 200
)

var subagentCSVMu sync.Mutex

// SubagentRunRecord 子 Agent 一次运行的 CSV 行。
type SubagentRunRecord struct {
	SessionID     string
	DelegateIndex int
	StartedAt     string
	FinishedAt    string
	ID            string
	Workspace     string
	OutputCwd     string
	Model         string
	Status        string
	Rounds        int
	Tools         string
	Task          string
	Context       string
	Summary       string
}

// SubagentRunsCSVPath 产出区对应 CSV：~/.cata/subagent_runs/<目录路径_/替换>.csv
func SubagentRunsCSVPath(outputCwd string) string {
	return filepath.Join(CataHome(), DirSubagentRuns, subagentCSVFileName(outputCwd))
}

// SubagentRunsCSVPathActive 当前 chat 产出区的 CSV 路径。
func SubagentRunsCSVPathActive() string {
	return SubagentRunsCSVPath(OutputCwd())
}

// subagentCSVFileName 将产出区绝对路径转为文件名（/ 与 \ → _，保留盘符等）。
func subagentCSVFileName(outputCwd string) string {
	cwd := strings.TrimSpace(outputCwd)
	if cwd == "" {
		return "_no_cwd.csv"
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	abs = filepath.Clean(abs)
	s := filepath.ToSlash(abs)
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, ":", "_")
	s = strings.Trim(s, "_")
	if s == "" {
		return "_root.csv"
	}
	if len(s) > maxSubagentName {
		s = s[:maxSubagentName]
	}
	return s + ".csv"
}

// AppendSubagentRunCSV 追加一行子 Agent 运行记录（并发安全）。
func AppendSubagentRunCSV(rec SubagentRunRecord) error {
	subagentCSVMu.Lock()
	defer subagentCSVMu.Unlock()

	path := SubagentRunsCSVPath(rec.OutputCwd)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	needHeader := true
	if st, err := os.Stat(path); err == nil && st.Size() > 0 {
		needHeader = false
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if needHeader {
		if err := w.Write(strings.Split(subagentCSVHeader, ",")); err != nil {
			return err
		}
	}
	row := []string{
		strings.TrimSpace(rec.SessionID),
		fmt.Sprintf("%d", rec.DelegateIndex),
		strings.TrimSpace(rec.StartedAt),
		strings.TrimSpace(rec.FinishedAt),
		strings.TrimSpace(rec.ID),
		strings.TrimSpace(rec.Workspace),
		strings.TrimSpace(rec.OutputCwd),
		strings.TrimSpace(rec.Model),
		strings.TrimSpace(rec.Status),
		fmt.Sprintf("%d", rec.Rounds),
		strings.TrimSpace(rec.Tools),
		capSubagentField(rec.Task),
		capSubagentField(rec.Context),
		capSubagentField(rec.Summary),
	}
	if err := w.Write(row); err != nil {
		return err
	}
	w.Flush()
	return w.Error()
}

func capSubagentField(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if len(s) <= maxSubagentField {
		return s
	}
	return s[:maxSubagentField] + "…"
}

func SubagentWorkspaceLabel() string {
	return SubagentWorkspaceLabelFor(Active())
}

// SubagentWorkspaceLabelFor 显式指定 workspace 的子 Agent 归属标识（多 chat 并行勿依赖全局 Active）。
func SubagentWorkspaceLabelFor(w *Workspace) string {
	if w != nil {
		return w.ID
	}
	return ""
}

func SubagentRunFinishedAt() string {
	return clock.RFC3339()
}

// AppendDelegateWaitNote 将 delegate_wait 摘要写入 short-term（全局 Active）。
func AppendDelegateWaitNote(summary string) error {
	w, err := MustActive()
	if err != nil {
		return err
	}
	return AppendDelegateWaitNoteFor(w, summary)
}

// AppendDelegateWaitNoteFor 显式指定 workspace 写入 delegate_wait 摘要（多 chat 并行勿依赖全局 Active）。
func AppendDelegateWaitNoteFor(w *Workspace, summary string) error {
	if w == nil {
		var err error
		w, err = MustActive()
		if err != nil {
			return err
		}
	}
	summary = truncateRunes(strings.TrimSpace(summary), 1200)
	if summary == "" {
		return nil
	}
	block := fmt.Sprintf("\n\n## %s delegate_wait\n\n%s\n", clock.RFC3339(), summary)
	return appendToShortTerm(w.ShortTermPath(), block)
}
