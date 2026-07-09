package client

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"cata/internal/config"
)

// subagentRecord 父 Agent 委派的一条子任务（TUI 侧栏与详情 overlay）。
type subagentRecord struct {
	ID      string
	Task    string
	Model   string
	Status  string // running | done | failed
	Summary string
	Log     string
}

func (m *model) findSubagent(id string) *subagentRecord {
	for i := range m.subagents {
		if m.subagents[i].ID == id {
			return &m.subagents[i]
		}
	}
	return nil
}

func (m *model) upsertSubagentStart(id, task, model string) {
	if rec := m.findSubagent(id); rec != nil {
		rec.Task = task
		rec.Model = model
		rec.Status = "running"
		return
	}
	m.subagents = append(m.subagents, subagentRecord{
		ID: id, Task: task, Model: model, Status: "running",
	})
	short := task
	if len(short) > 72 {
		short = short[:72] + "…"
	}
	m.appendLog(styleTool.Render(fmt.Sprintf("\n▸ [子任务 %s] 父 Agent 委派: %s", id, short))+"\n", true)
	m.appendLog(styleDim.Render("  （侧栏「委托」可点击查看；或按 d）\n"), true)
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

func (m *model) finishSubagent(id string, success bool, summary string) {
	rec := m.findSubagent(id)
	if rec == nil {
		return
	}
	if success {
		rec.Status = "done"
	} else {
		rec.Status = "failed"
	}
	rec.Summary = summary
	m.syncSidebarViewport()
}

func (m *model) handleSubagentStream(kind string, raw map[string]any) {
	id := str(raw["id"])
	switch kind {
	case "subagent_start":
		m.upsertSubagentStart(id, str(raw["task"]), str(raw["model"]))
		m.syncSidebarViewport()
	case "subagent_queued":
		short := str(raw["task"])
		if len(short) > 48 {
			short = short[:48] + "…"
		}
		m.appendLog(styleDim.Render(fmt.Sprintf("  ⏳ [%s] 排队等待 worker 槽位: %s\n", id, short)), true)
		m.syncSidebarViewport()
	case "subagent_progress":
		m.appendSubagentLog(id, "• "+str(raw["message"]))
	case "subagent_tool":
		phase := str(raw["phase"])
		name := str(raw["name"])
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

func (m *model) appendDelegateSidebarSections(lines []string, innerW int) []string {
	if len(m.subagents) == 0 {
		return lines
	}
	running := 0
	for _, sa := range m.subagents {
		if sa.Status == "running" {
			running++
		}
	}
	label := "委托"
	if running > 0 {
		max := 4
		if cfg := config.Config; cfg != nil && cfg.Subagent.MaxConcurrent > 0 {
			max = cfg.Subagent.MaxConcurrent
		}
		label = fmt.Sprintf("委托 %d/%d", running, max)
	}
	var body []string
	for _, sa := range m.subagents {
		st := sa.Status
		switch st {
		case "running":
			st = "进行中"
		case "done":
			st = "完成"
		case "failed":
			st = "失败"
		}
		body = append(body, fmt.Sprintf("▸ %s  %s", sa.ID, st))
	}
	return appendSidebarSection(lines, innerW, sidebarSection{label: label, lines: body}, true)
}

func (m *model) openSubagentPicker() (tea.Model, tea.Cmd) {
	if len(m.subagents) == 0 {
		return m, nil
	}
	var items []list.Item
	for _, sa := range m.subagents {
		title := sa.ID + "  " + sa.Status
		desc := sa.Task
		if len(desc) > 60 {
			desc = desc[:60] + "…"
		}
		items = append(items, pickItem{id: sa.ID, title: title, desc: desc})
	}
	l := list.New(items, list.NewDefaultDelegate(), 56, min(10, len(items)+3))
	l.SetShowTitle(true)
	l.Title = "子 Agent（父 Agent 委派）"
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
	b.WriteString(fmt.Sprintf("id: %s  model: %s  status: %s\n\n", rec.ID, rec.Model, rec.Status))
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

func (m *model) subagentIDAtSidebarLine(lineIdx int) string {
	if lineIdx < 0 {
		return ""
	}
	lines := strings.Split(m.sidebarText(), "\n")
	if lineIdx >= len(lines) {
		return ""
	}
	line := strings.TrimSpace(lines[lineIdx])
	if !strings.HasPrefix(line, "▸ sub-") {
		return ""
	}
	fields := strings.Fields(line)
	if len(fields) >= 2 {
		return fields[1]
	}
	return ""
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
