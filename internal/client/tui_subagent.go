package client

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// subagentRecord 父 Agent 委派的一条子任务（TUI 侧栏与详情 overlay）。
type subagentRecord struct {
	ID       string
	Task     string
	Model    string
	Profile  string
	Status   string // queued | running
	Round    int
	LastTool string
	Summary  string
	Log      string
}

func sidebarProfileLabel(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "task"
	}
	return p
}

func (m *model) sidebarMainAgentState() string {
	if m.streaming {
		st := strings.TrimSpace(m.stats.state)
		if strings.HasPrefix(st, "model round") {
			return st
		}
		if lt := strings.TrimSpace(m.stats.lastTool); lt != "" {
			return lt
		}
		return "流式"
	}
	switch strings.TrimSpace(m.stats.state) {
	case "", "ready":
		return "就绪"
	case "thinking":
		return "思考"
	default:
		return stTrimShort(m.stats.state, 18)
	}
}

func stTrimShort(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func (sa *subagentRecord) sidebarState() string {
	switch sa.Status {
	case "queued":
		return "排队"
	}
	if t := strings.TrimSpace(sa.LastTool); t != "" {
		return t
	}
	if sa.Round > 0 {
		return fmt.Sprintf("r%d", sa.Round)
	}
	return "运行"
}

func (m *model) appendAgentsSidebarSection(lines []string, innerW int) []string {
	show := m.streaming || m.stats.round > 0 || len(m.subagents) > 0 || m.stats.promptProfile != ""
	if !show {
		return lines
	}
	var body []string
	mainProf := sidebarProfileLabel(m.stats.promptProfile)
	body = append(body, fmt.Sprintf("主agent  %s  %s", mainProf, m.sidebarMainAgentState()))
	for _, sa := range m.subagents {
		if sa.Status != "queued" && sa.Status != "running" {
			continue
		}
		prof := sidebarProfileLabel(sa.Profile)
		if prof == "task" && sa.Profile == "" {
			prof = "minimal"
		}
		body = append(body, fmt.Sprintf("\t子agent  %s  %s  %s", prof, sa.sidebarState(), sa.ID))
	}
	return appendSidebarSection(lines, innerW, sidebarSection{label: "Agent", lines: body}, true)
}

func (m *model) findSubagent(id string) *subagentRecord {
	for i := range m.subagents {
		if m.subagents[i].ID == id {
			return &m.subagents[i]
		}
	}
	return nil
}

func (m *model) upsertSubagent(id, task, model, profile, status string) {
	if rec := m.findSubagent(id); rec != nil {
		if task != "" {
			rec.Task = task
		}
		if model != "" {
			rec.Model = model
		}
		if profile != "" {
			rec.Profile = profile
		}
		if status != "" {
			rec.Status = status
		}
		return
	}
	if profile == "" {
		profile = "minimal"
	}
	if status == "" {
		status = "running"
	}
	m.subagents = append(m.subagents, subagentRecord{
		ID: id, Task: task, Model: model, Profile: profile, Status: status,
	})
}

func (m *model) upsertSubagentStart(id, task, model, profile string) {
	m.upsertSubagent(id, task, model, profile, "running")
	short := task
	if len(short) > 72 {
		short = short[:72] + "…"
	}
	m.appendLog(styleTool.Render(fmt.Sprintf("\n▸ [子任务 %s] 父 Agent 委派: %s", id, short))+"\n", true)
	m.appendLog(styleDim.Render("  （侧栏 Agent 区可点击查看；或按 d）\n"), true)
}

func (m *model) removeSubagent(id string) {
	for i := range m.subagents {
		if m.subagents[i].ID == id {
			m.subagents = append(m.subagents[:i], m.subagents[i+1:]...)
			return
		}
	}
}

func (m *model) appendSubagentLog(id, line string) {
	rec := m.findSubagent(id)
	if rec == nil {
		return
	}
	rec.Log += line
	if !strings.HasSuffix(rec.Log, "\n") {
		rec.Log += "\n"
	}
}

func parseSubagentRound(msg string) int {
	msg = strings.TrimSpace(msg)
	var n int
	if _, err := fmt.Sscanf(msg, "round %d", &n); err == nil && n > 0 {
		return n
	}
	return 0
}

func (m *model) finishSubagent(id string, success bool, summary string) {
	rec := m.findSubagent(id)
	if rec == nil {
		return
	}
	st := "完成"
	if !success {
		st = "失败"
	}
	short := strings.TrimSpace(summary)
	if len(short) > 80 {
		short = short[:80] + "…"
	}
	m.appendLog(styleDim.Render(fmt.Sprintf("  [%s] 子 Agent %s: %s\n", id, st, short)), true)
	m.removeSubagent(id)
	m.syncSidebarViewport()
}

func (m *model) handleSubagentStream(kind string, raw map[string]any) {
	id := str(raw["id"])
	profile := str(raw["prompt_profile"])
	switch kind {
	case "subagent_start":
		m.upsertSubagentStart(id, str(raw["task"]), str(raw["model"]), profile)
		m.syncSidebarViewport()
	case "subagent_queued":
		m.upsertSubagent(id, str(raw["task"]), "", profile, "queued")
		short := str(raw["task"])
		if len(short) > 48 {
			short = short[:48] + "…"
		}
		m.appendLog(styleDim.Render(fmt.Sprintf("  ⏳ [%s] 排队等待 worker 槽位: %s\n", id, short)), true)
		m.syncSidebarViewport()
	case "subagent_progress":
		if rec := m.findSubagent(id); rec != nil {
			if r := parseSubagentRound(str(raw["message"])); r > 0 {
				rec.Round = r
			}
			if profile != "" {
				rec.Profile = profile
			}
			if rec.Status == "queued" {
				rec.Status = "running"
			}
		}
		m.appendSubagentLog(id, "• "+str(raw["message"]))
		m.syncSidebarViewport()
	case "subagent_tool":
		phase := str(raw["phase"])
		name := str(raw["name"])
		if rec := m.findSubagent(id); rec != nil && phase == "start" && name != "" {
			rec.LastTool = name
			m.syncSidebarViewport()
		}
		if phase == "start" {
			m.appendSubagentLog(id, "  ▸ "+name)
		} else {
			out := str(raw["output"])
			if out != "" {
				m.appendSubagentLog(id, "    "+strings.TrimSpace(out))
			}
		}
	case "subagent_done":
		ok, _ := raw["success"].(bool)
		m.finishSubagent(id, ok, str(raw["summary"]))
	}
}

func (m *model) openSubagentPicker() (tea.Model, tea.Cmd) {
	var running []subagentRecord
	for _, sa := range m.subagents {
		if sa.Status == "queued" || sa.Status == "running" {
			running = append(running, sa)
		}
	}
	if len(running) == 0 {
		return m, nil
	}
	var items []list.Item
	for _, sa := range running {
		title := sa.ID + "  " + sa.sidebarState()
		desc := sa.Task
		if len(desc) > 60 {
			desc = desc[:60] + "…"
		}
		items = append(items, pickItem{id: sa.ID, title: title, desc: desc})
	}
	l := list.New(items, list.NewDefaultDelegate(), 56, min(10, len(items)+3))
	l.SetShowTitle(true)
	l.Title = "子 Agent（运行中）"
	l.SetFilteringEnabled(false)
	m.overlay = &overlayState{mode: overlaySubagentPick, list: l}
	return m, nil
}

func (m *model) openSubagentView(id string) (tea.Model, tea.Cmd) {
	rec := m.findSubagent(id)
	if rec == nil {
		return m, nil
	}
	var b strings.Builder
	b.WriteString("父 Agent 委派任务\n")
	b.WriteString(strings.Repeat("─", 32))
	b.WriteString("\n\n")
	b.WriteString(rec.Task)
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("id: %s  model: %s  profile: %s  status: %s\n\n", rec.ID, rec.Model, sidebarProfileLabel(rec.Profile), rec.sidebarState()))
	if rec.Log != "" {
		b.WriteString("工作记录\n")
		b.WriteString(strings.Repeat("─", 32))
		b.WriteString("\n")
		b.WriteString(rec.Log)
		b.WriteString("\n")
	}
	if rec.Summary != "" {
		b.WriteString("\n结果摘要\n")
		b.WriteString(strings.Repeat("─", 32))
		b.WriteString("\n")
		b.WriteString(rec.Summary)
		b.WriteString("\n")
	}
	vp := viewport.New(64, 18)
	vp.SetContent(b.String())
	m.overlay = &overlayState{mode: overlaySubagentView, subagentID: id, subagentVP: vp}
	return m, nil
}

func subagentIDFromSidebarLine(line string) string {
	line = strings.TrimSpace(line)
	for _, f := range strings.Fields(line) {
		if strings.HasPrefix(f, "sub-") {
			return f
		}
	}
	return ""
}

func (m *model) subagentIDAtSidebarLine(lineIdx int) string {
	if lineIdx < 0 {
		return ""
	}
	lines := strings.Split(m.sidebarText(), "\n")
	if lineIdx >= len(lines) {
		return ""
	}
	return subagentIDFromSidebarLine(lines[lineIdx])
}

func (m *model) handleSidebarClick(msg tea.MouseMsg) (tea.Model, tea.Cmd, bool) {
	if !sidebarActive(m.width) || m.overlay != nil {
		return m, nil, false
	}
	if msg.Action != tea.MouseActionPress {
		return m, nil, false
	}
	pl := m.paneLayout()
	if pl.hit(msg.X, msg.Y) != hoverSidebar {
		return m, nil, false
	}
	lineIdx := m.sidebarVP.YOffset + msg.Y
	if id := m.subagentIDAtSidebarLine(lineIdx); id != "" {
		nm, cmd := m.openSubagentView(id)
		return nm, cmd, true
	}
	return m, nil, true
}

func (m *model) renderSubagentOverlay() string {
	if m.overlay == nil {
		return ""
	}
	switch m.overlay.mode {
	case overlaySubagentPick:
		return lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("205")).
			Padding(1, 2).
			Render(m.overlay.list.View())
	case overlaySubagentView:
		title := "子 Agent · " + m.overlay.subagentID
		body := m.overlay.subagentVP.View()
		help := styleDim.Render("↑↓ scroll · esc 关闭")
		return lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("39")).
			Padding(1, 2).
			Width(72).
			Render(title + "\n\n" + body + "\n" + help)
	}
	return ""
}
