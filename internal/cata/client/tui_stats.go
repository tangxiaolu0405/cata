package client

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
	"cata/internal/llm"
)

func bindStats(cwd string) {
	_, _ = brain.ResolveWorkspace(cwd)
	if w := brain.Active(); w != nil {
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
	if s, ok := ev["project_cata"].(string); ok && s != "" {
		m.stats.projectCata = s
	}
	if s, ok := ev["cata_home"].(string); ok && s != "" {
		m.stats.cataHome = s
	}
	if s, ok := ev["output_cwd"].(string); ok && s != "" {
		m.stats.outputCwd = s
	}
	if s, ok := ev["active_mode"].(string); ok && s != "" {
		m.stats.mode = s
	}
	if s, ok := ev["model"].(string); ok && s != "" {
		m.stats.chatModel = s
	}
	if s, ok := ev["prompt_profile"].(string); ok && s != "" {
		m.stats.promptProfile = s
	}
	if v, ok := ev["subagent_running"].(float64); ok {
		m.stats.subagentRunning = int(v)
	}
	if v, ok := ev["subagent_max"].(float64); ok {
		m.stats.subagentMax = int(v)
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

type sidebarSection struct {
	label string
	lines []string
}

func sidebarInnerWidth() int {
	// styleSidebar: padding 0,1 → 左右各 1 列
	const pad = 2
	w := sidebarWidth - pad
	if w < 24 {
		return 24
	}
	return w
}

func sidebarDivider(w int) string {
	if w < 4 {
		w = 4
	}
	return styleDim.Render(strings.Repeat("─", w))
}

func wrapSidebar(width int, text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	rendered := lipgloss.NewStyle().Width(width).Render(text)
	var out []string
	for _, ln := range strings.Split(rendered, "\n") {
		out = append(out, strings.TrimRight(ln, " "))
	}
	return out
}

func appendSidebarSection(all []string, innerW int, sec sidebarSection, withDivider bool) []string {
	if len(sec.lines) == 0 {
		return all
	}
	if withDivider && len(all) > 0 {
		all = append(all, sidebarDivider(innerW))
	}
	all = append(all, styleSidebarLabel.Render(sec.label))
	for _, line := range sec.lines {
		all = append(all, wrapSidebar(innerW, line)...)
	}
	return all
}

func (m *model) sidebarText() string {
	if !sidebarActive(m.width) {
		return ""
	}
	innerW := sidebarInnerWidth()
	var lines []string
	lines = append(lines, styleDim.Render("cata"))

	lines = m.appendContextSidebarSections(lines, innerW)
	lines = m.appendRunSidebarSection(lines, innerW)
	lines = m.appendAgentsSidebarSection(lines, innerW)
	lines = m.appendActivitySidebarSections(lines, innerW)

	if len(lines) > 1 {
		lines = append(lines, sidebarDivider(innerW))
		lines = append(lines, styleDim.Render("/status 环境与工具"))
	}

	return strings.Join(lines, "\n")
}

func sidebarShortPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimRight(p, "/")
	parts := strings.Split(p, "/")
	if len(parts) <= 2 {
		return p
	}
	return strings.Join(parts[len(parts)-2:], "/")
}

func sidebarShortModel(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if len(name) <= 22 {
		return name
	}
	return name[:20] + "…"
}

func sidebarModelLine(chatModel string) string {
	byRole := llm.ModelsByRole()
	chat := strings.TrimSpace(chatModel)
	if chat == "" {
		chat = byRole["chat"]
	}
	if chat == "" {
		return ""
	}
	line := sidebarShortModel(chat)
	var alt []string
	if evo := byRole["evolution"]; evo != "" && evo != chat {
		alt = append(alt, "evo:"+sidebarShortModel(evo))
	}
	if w := byRole["worker"]; w != "" && w != chat && w != byRole["evolution"] {
		alt = append(alt, "wrk:"+sidebarShortModel(w))
	}
	if len(alt) > 0 {
		line += " · " + strings.Join(alt, " ")
	}
	return line
}

func sidebarFormatTokens(n int) string {
	if n <= 0 {
		return ""
	}
	if n >= 1000 {
		return fmt.Sprintf("~%dk tok", (n+500)/1000)
	}
	return fmt.Sprintf("~%d tok", n)
}

func (m *model) appendContextSidebarSections(lines []string, innerW int) []string {
	var body []string
	if home := sidebarShortPath(m.stats.cataHome); home != "" {
		body = append(body, "home "+home)
	}
	if pc := sidebarShortPath(m.stats.projectCata); pc != "" {
		body = append(body, ".cata "+pc)
	}
	path := sidebarShortPath(m.stats.focusPath)
	if path == "" {
		path = sidebarShortPath(m.stats.outputCwd)
	}
	if path != "" {
		body = append(body, "focus "+path)
	}
	if out := sidebarShortPath(m.stats.outputCwd); out != "" && out != path {
		body = append(body, "产出 "+out)
	}
	if m.stats.mode != "" {
		body = append(body, m.stats.mode)
	}
	if model := sidebarModelLine(m.stats.chatModel); model != "" {
		body = append(body, model)
	}
	if len(body) == 0 {
		return lines
	}
	return appendSidebarSection(lines, innerW, sidebarSection{"三根/上下文", body}, true)
}

// appendRunSidebarSection 侧栏「运行」区：一行概要 + 一行运行状态（随任务变化）。
// 点击「状态」行会打开运行详情 overlay（见 tui_status.go）。
func (m *model) appendRunSidebarSection(lines []string, innerW int) []string {
	show := m.streaming || m.stats.round > 0 || strings.TrimSpace(m.stats.runSummary) != ""
	if !show {
		return lines
	}
	var body []string
	if s := m.runSummaryLine(); s != "" {
		body = append(body, "概要 "+s)
	}
	body = append(body, "状态 "+m.statusOneLine())
	return appendSidebarSection(lines, innerW, sidebarSection{label: "运行", lines: body}, true)
}

func (m *model) appendActivitySidebarSections(lines []string, innerW int) []string {
	var body []string
	if m.stats.round > 0 || m.streaming {
		act := fmt.Sprintf("r%d", m.stats.round)
		state := strings.TrimSpace(m.stats.state)
		switch {
		case m.streaming:
			act += " · 流式"
		case state != "" && state != "ready":
			act += " · " + state
		}
		body = append(body, act)
	}
	if m.stats.lastTool != "" && (m.streaming || m.stats.state == "tool") {
		body = append(body, m.stats.lastTool)
	}
	if tok := sidebarFormatTokens(m.stats.sessionTok); tok != "" {
		body = append(body, tok)
	}
	if len(body) == 0 {
		return lines
	}
	return appendSidebarSection(lines, innerW, sidebarSection{"活动", body}, true)
}

func (m *model) statusDump() string {
	m.loadEvolve()
	var b strings.Builder
	b.WriteString("── status ──\n")
	if m.stats.wsID != "" {
		b.WriteString("ws: " + m.stats.wsID + "\n")
	}
	if m.stats.focusPath != "" {
		b.WriteString("focus: " + m.stats.focusPath + "\n")
	}
	b.WriteString("out: " + m.stats.outputCwd + "\n")
	if m.stats.mode != "" {
		b.WriteString("mode: " + m.stats.mode + "\n")
	}
	if m.stats.evolveOn {
		b.WriteString(fmtEvolveLine(m.stats.evolveSec) + "\n")
	} else {
		b.WriteString("evolve: off\n")
	}
	if m.stats.evolveLast != "" {
		b.WriteString("evolve last: " + m.stats.evolveLast + "\n")
	}
	b.WriteString(fmt.Sprintf("round %d turns %d tools %d\n", m.stats.round, m.stats.turns, m.stats.tools))
	if cfg := config.Config; cfg != nil {
		b.WriteString(cfg.LLM.Provider + " / " + cfg.LLM.APIFormat + " / " + cfg.LLM.Model + "\n")
		if byRole := llm.ModelsByRole(); len(byRole) > 0 {
			b.WriteString("models: ")
			var parts []string
			for _, role := range []string{"chat", "evolution", "worker", "summarize"} {
				if m := byRole[role]; m != "" {
					parts = append(parts, role+"="+m)
				}
			}
			b.WriteString(strings.Join(parts, " "))
			b.WriteString("\n")
		}
	}
	e := m.runtime
	b.WriteString("\n── environment ──\n")
	b.WriteString(fmt.Sprintf("host: %s  command: %s  arch: %s\n", e.HostPlatform(), e.CommandPlatform(), e.Arch))
	b.WriteString(fmt.Sprintf("terminal: %s\n", e.Terminal))
	b.WriteString(fmt.Sprintf("shell: %s", e.Shell))
	if e.ShellPath != "" {
		b.WriteString(" (" + e.ShellPath + ")")
	}
	b.WriteString("\n")
	b.WriteString("shell syntax: " + e.ShellSyntaxLabel() + "\n")
	b.WriteString("\n" + e.ToolsAvailabilityBlock())
	if cfg := config.Config; cfg != nil {
		b.WriteString("\n── cata tools ──\n")
		b.WriteString("run_command: " + sidebarOnOff(cfg.Exec.Enabled) + "\n")
		b.WriteString("files: " + sidebarOnOff(cfg.WorkspaceFilesEnabled()) + "\n")
		b.WriteString("browser mcp: " + sidebarOnOff(cfg.MCP.Enabled) + "\n")
	}
	b.WriteString("\n" + brain.ServerRegisteredToolsBlock())
	b.WriteString("\nsubagent log: " + brain.SubagentRunsCSVPathActive() + "\n")
	return b.String()
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

func sidebarOnOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
