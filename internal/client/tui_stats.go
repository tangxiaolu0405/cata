package client

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"cata/internal/brain"
	"cata/internal/config"
)

func bindStats(cwd string) {
	_, _ = brain.ResolveWorkspace(cwd)
	if w := brain.Active(); w != nil {
		// stats filled on first model init via applyStats
		_ = w
	}
}

func (m *model) applyStats(ev map[string]any) {
	if v, ok := ev["round"].(float64); ok {
		m.stats.round = int(v)
	}
	if v, ok := ev["turn"].(float64); ok {
		m.stats.turns = int(v)
	}
	if v, ok := ev["tools"].(float64); ok {
		m.stats.tools = int(v)
	}
	if s, ok := ev["last_tool"].(string); ok {
		m.stats.lastTool = s
	}
	if s, ok := ev["state"].(string); ok {
		m.stats.state = s
	}
	var tok int
	if v, ok := ev["session_prompt"].(float64); ok {
		tok += int(v)
	}
	if v, ok := ev["session_completion"].(float64); ok {
		tok += int(v)
	}
	if tok > 0 {
		m.stats.sessionTok = tok
	}
	if v, ok := ev["context_est"].(float64); ok {
		m.stats.contextEst = int(v)
	}
	if s, ok := ev["workspace_id"].(string); ok && s != "" {
		m.stats.wsID = s
	}
	if s, ok := ev["focus_path"].(string); ok && s != "" {
		m.stats.focusPath = s
	}
	if s, ok := ev["output_cwd"].(string); ok && s != "" {
		m.stats.outputCwd = s
	}
	if s, ok := ev["active_mode"].(string); ok && s != "" {
		m.stats.mode = s
	}
	m.loadEvolve()
}

func (m *model) loadEvolve() {
	m.stats.evolveOn = false
	m.stats.evolveSec = 0
	m.stats.evolveLast = ""
	if cfg := config.Config; cfg != nil {
		m.stats.evolveOn = cfg.Evolution.Enabled
		m.stats.evolveSec = cfg.Evolution.CycleInterval
	}
	w := brain.Active()
	if w == nil {
		return
	}
	data, err := os.ReadFile(w.EvolutionLogPath())
	if err != nil {
		return
	}
	var log struct {
		Entries []struct {
			Action string `json:"action"`
		} `json:"entries"`
	}
	if json.Unmarshal(data, &log) != nil || len(log.Entries) == 0 {
		return
	}
	m.stats.evolveLast = strings.TrimSpace(log.Entries[len(log.Entries)-1].Action)
}

func (m *model) sidebarText() string {
	if os.Getenv("CATA_NO_SIDEBAR") != "" {
		return ""
	}
	if m.width < minMainWidth+sidebarWidth {
		return ""
	}
	var lines []string
	lines = append(lines, "── cata ──")
	if m.stats.evolveOn {
		lines = append(lines, fmtEvolveLine(m.stats.evolveSec))
	} else {
		lines = append(lines, "evolve off")
	}
	if m.stats.wsID != "" {
		lines = append(lines, "ws "+trunc(m.stats.wsID, 22))
	}
	if m.stats.outputCwd != "" {
		lines = append(lines, trunc(m.stats.outputCwd, 24))
	}
	if m.stats.round > 0 {
		lines = append(lines, fmt.Sprintf("r%d t%d", m.stats.round, m.stats.turns))
	}
	if m.stats.sessionTok > 0 {
		lines = append(lines, fmt.Sprintf("~%d tok", m.stats.sessionTok))
	}
	if m.stats.lastTool != "" {
		lines = append(lines, trunc(m.stats.lastTool, 24))
	}
	if m.stats.state != "" && m.stats.state != "ready" {
		lines = append(lines, trunc(m.stats.state, 24))
	}
	return strings.Join(lines, "\n")
}

func fmtEvolveLine(sec int) string {
	if sec <= 0 {
		sec = 600
	}
	if sec < 60 {
		return fmt.Sprintf("evolve %ds", sec)
	}
	if sec%60 == 0 {
		return fmt.Sprintf("evolve %dm", sec/60)
	}
	return fmt.Sprintf("evolve %ds", sec)
}
