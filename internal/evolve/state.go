package evolve

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"cata/internal/brain"
	"cata/internal/clock"
)

// Snapshot 自主演进 Observe 阶段的只读状态（仅元数据，不把整库塞进 LLM）。
type Snapshot struct {
	ObservedAt            string   `json:"observed_at"`
	WorkspaceID           string   `json:"workspace_id,omitempty"`
	FocusPath             string   `json:"focus_path,omitempty"`
	WorkspaceName         string   `json:"workspace_name,omitempty"`
	HotModTime            string   `json:"hot_mod_time,omitempty"`
	PersonaBytes          int64    `json:"persona_bytes,omitempty"`
	PersonaLocalBytes     int64    `json:"persona_local_bytes,omitempty"`
	ShortTermModTime      string   `json:"short_term_mod_time,omitempty"`
	ShortTermBytes        int64    `json:"short_term_bytes"`
	LongTermFileCount     int      `json:"long_term_file_count"`
	ArchiveFileCount      int      `json:"archive_file_count"`
	LastEvolutionAt       string   `json:"last_evolution_at,omitempty"`
	LastEvolutionAction   string   `json:"last_evolution_action,omitempty"`
	RecentLogSummary      string   `json:"recent_log_summary,omitempty"`
	Triggers              []string `json:"triggers,omitempty"`
	SkillIDs              []string `json:"skill_ids,omitempty"`
}

// Fingerprint 仅跟踪演进「输入」信号（不含 hot：hot 由演进写出，不应作为触发依据）。
func (s *Snapshot) Fingerprint() string {
	return fmt.Sprintf("st:%s|sb:%d|lt:%d",
		s.ShortTermModTime, s.ShortTermBytes, s.LongTermFileCount)
}

// Observe 读取指定 workspace 脑子元数据（不读 workflow/core 全文）。
func Observe(ws *brain.Workspace) (*Snapshot, error) {
	if ws == nil {
		var err error
		ws, err = brain.MustActive()
		if err != nil {
			return nil, err
		}
	}
	s := &Snapshot{
		ObservedAt:    clock.RFC3339(),
		WorkspaceID:   ws.ID,
		FocusPath:     ws.RootPath,
		WorkspaceName: ws.Name,
	}

	if info, err := os.Stat(ws.PersonaPath()); err == nil {
		s.HotModTime = clock.FormatTime(info.ModTime(), time.RFC3339)
		s.PersonaBytes = info.Size()
	}
	if info, err := os.Stat(ws.PersonaLocalPath()); err == nil {
		s.PersonaLocalBytes = info.Size()
	}

	shortPath := ws.ShortTermPath()
	if info, err := os.Stat(shortPath); err == nil {
		s.ShortTermModTime = clock.FormatTime(info.ModTime(), time.RFC3339)
	}
	if data, err := os.ReadFile(shortPath); err == nil {
		s.ShortTermBytes = int64(len(data))
	}

	if entries, err := os.ReadDir(ws.LongTermDir()); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			if brain.IsLongMemoryCanonicalFile(e.Name()) {
				continue
			}
			s.LongTermFileCount++
		}
	}

	if entries, err := os.ReadDir(ws.ArchiveDir()); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				s.ArchiveFileCount++
			}
		}
	}

	loadLastEvolutionMeta(s, ws.EvolutionLogPath())
	s.RecentLogSummary = summarizeRecentLog(ws.EvolutionLogPath(), 2, 80)
	if ids, err := brain.ListWorkspaceSkillIDs(ws); err == nil {
		s.SkillIDs = ids
	}
	computeTriggers(s, ws)
	return s, nil
}

func loadLastEvolutionMeta(s *Snapshot, logPath string) {
	data, err := os.ReadFile(logPath)
	if err != nil {
		return
	}
	var log EvolutionLog
	if err := json.Unmarshal(data, &log); err != nil || len(log.Entries) == 0 {
		return
	}
	last := log.Entries[len(log.Entries)-1]
	s.LastEvolutionAt = last.Timestamp
	s.LastEvolutionAction = last.Action
}

func summarizeRecentLog(logPath string, n int, maxLearning int) string {
	data, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	var log EvolutionLog
	if err := json.Unmarshal(data, &log); err != nil || len(log.Entries) == 0 {
		return ""
	}
	start := len(log.Entries) - n
	if start < 0 {
		start = 0
	}
	var b strings.Builder
	for i := start; i < len(log.Entries); i++ {
		e := log.Entries[i]
		b.WriteString(e.Action)
		if e.Learning != "" {
			b.WriteString(": ")
			b.WriteString(truncate(e.Learning, maxLearning))
		}
		if i < len(log.Entries)-1 {
			b.WriteString(" | ")
		}
	}
	return b.String()
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
