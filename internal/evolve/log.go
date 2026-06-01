package evolve

import (
	"encoding/json"
	"fmt"
	"os"

	"cata/internal/brain"
	"cata/internal/clock"
)

// EvolutionLog 单 workspace 演进日志。
type EvolutionLog struct {
	Entries []LogEntry `json:"entries"`
}

// LogEntry 单条演进记录。
type LogEntry struct {
	Timestamp   string   `json:"timestamp"`
	WorkspaceID string   `json:"workspace_id,omitempty"`
	ModeID      string   `json:"mode_id,omitempty"`
	Action      string   `json:"action"`
	Reason      string   `json:"reason"`
	Status      string   `json:"status"`
	Learning    string   `json:"learning"`
	DocTouched  []string `json:"doc_touched,omitempty"`
}

// AppendLog 向指定 workspace 的 evolution_log.json 追加。
func AppendLog(ws *brain.Workspace, entry LogEntry) error {
	if ws == nil {
		return fmt.Errorf("workspace required")
	}
	path := ws.EvolutionLogPath()
	var log EvolutionLog
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &log)
	}
	if log.Entries == nil {
		log.Entries = []LogEntry{}
	}
	if entry.Timestamp == "" {
		entry.Timestamp = clock.RFC3339()
	}
	if entry.Status == "" {
		entry.Status = "completed"
	}
	if entry.WorkspaceID == "" {
		entry.WorkspaceID = ws.ID
	}
	log.Entries = append(log.Entries, entry)
	if len(log.Entries) > maxLogEntries {
		log.Entries = log.Entries[len(log.Entries)-maxLogEntries:]
	}

	data, err := json.MarshalIndent(log, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
