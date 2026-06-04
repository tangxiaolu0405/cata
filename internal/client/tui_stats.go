package client

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"cata/internal/brain"
	"cata/internal/config"
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

	lines = m.appendEnvSidebarSections(lines, innerW)
	lines = m.appendSessionSidebarSections(lines, innerW)
	lines = m.appendActivitySidebarSections(lines, innerW)

	return strings.Join(lines, "\n")
}

func (m *model) appendEnvSidebarSections(lines []string, innerW int) []string {
	e := m.runtime
	first := len(lines) <= 1

	plat := []string{
		"宿主平台  " + e.HostPlatform(),
		"命令平台  " + e.CommandPlatform(),
		"架构      " + e.Arch,
	}
	lines = appendSidebarSection(lines, innerW, sidebarSection{"平台", plat}, !first)
	first = false

	shell := []string{"类型  " + e.Shell}
	if e.ShellPath != "" {
		shell = append(shell, e.ShellPath)
	}
	shell = append(shell, "语法  "+e.ShellSyntaxLabel())
	lines = appendSidebarSection(lines, innerW, sidebarSection{"Shell", shell}, true)

	if e.Terminal != "" {
		lines = appendSidebarSection(lines, innerW, sidebarSection{"终端", []string{e.Terminal}}, true)
	}

	lines = appendSidebarSection(lines, innerW, sidebarSection{"PATH 工具", e.Tools.SidebarToolLines()}, true)
	lines = appendSidebarSection(lines, innerW, sidebarSection{"Cata 工具", m.cataToolsSidebarLines()}, true)

	return lines
}

func (m *model) cataToolsSidebarLines() []string {
	cfg := config.Config
	if cfg == nil {
		return []string{"（配置未加载）"}
	}
	return []string{
		"run_command  " + sidebarOnOff(cfg.Exec.Enabled),
		"文件工具     " + sidebarOnOff(cfg.WorkspaceFilesEnabled()),
		"browser MCP  " + sidebarOnOff(cfg.MCP.Enabled),
		"run_skill    开",
	}
}

func sidebarOnOff(on bool) string {
	if on {
		return "开"
	}
	return "关"
}

func (m *model) appendSessionSidebarSections(lines []string, innerW int) []string {
	var body []string
	if m.stats.wsID != "" {
		body = append(body, "workspace  "+m.stats.wsID)
	}
	if m.stats.focusPath != "" {
		body = append(body, m.stats.focusPath)
	}
	if m.stats.outputCwd != "" {
		body = append(body, "产出区  "+m.stats.outputCwd)
	}
	if m.stats.mode != "" {
		body = append(body, "mode  "+m.stats.mode)
	}
	if m.stats.evolveOn {
		body = append(body, fmtEvolveLine(m.stats.evolveSec))
	} else {
		body = append(body, "evolve  关")
	}
	if len(body) == 0 {
		return lines
	}
	return appendSidebarSection(lines, innerW, sidebarSection{"会话", body}, true)
}

func (m *model) appendActivitySidebarSections(lines []string, innerW int) []string {
	var body []string
	if m.stats.round > 0 || m.stats.turns > 0 {
		body = append(body, fmt.Sprintf("round %d  turns %d", m.stats.round, m.stats.turns))
	}
	if m.stats.sessionTok > 0 {
		body = append(body, fmt.Sprintf("tokens  ~%d", m.stats.sessionTok))
	}
	if m.stats.lastTool != "" {
		body = append(body, "last  "+m.stats.lastTool)
	}
	if m.stats.state != "" && m.stats.state != "ready" {
		body = append(body, "state  "+m.stats.state)
	}
	if len(body) == 0 {
		return lines
	}
	return appendSidebarSection(lines, innerW, sidebarSection{"本轮", body}, true)
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
	b.WriteString(fmt.Sprintf("round %d turns %d tools %d\n", m.stats.round, m.stats.turns, m.stats.tools))
	if cfg := config.Config; cfg != nil {
		b.WriteString(cfg.LLM.Provider + " / " + cfg.LLM.Model + "\n")
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
	b.WriteString("\n" + brain.ServerRegisteredToolsBlock())
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
