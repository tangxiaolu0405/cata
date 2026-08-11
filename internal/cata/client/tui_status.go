package client

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// runDetailMax 运行细节缓冲上限（环形，超出丢弃最旧行）。
const runDetailMax = 400

// statusSummaryMax 概要一行的最大可见宽度（超出截断加 …）。
const statusSummaryMax = 34

// appendRunDetail 追加一行运行细节（供「点击运行状态 → 运行详情」查看），不写主区。
func (m *model) appendRunDetail(line string) {
	line = strings.TrimRight(line, "\n")
	if line == "" {
		return
	}
	for _, ln := range strings.Split(line, "\n") {
		ln = strings.TrimRight(ln, " ")
		if ln == "" {
			continue
		}
		m.stats.runDetails = append(m.stats.runDetails, ln)
	}
	if len(m.stats.runDetails) > runDetailMax {
		m.stats.runDetails = m.stats.runDetails[len(m.stats.runDetails)-runDetailMax:]
	}
}

// setRunSummary 更新一行概要（取摘要，缺省取 message 首行）。
func (m *model) setRunSummary(summary, message string) {
	s := strings.TrimSpace(summary)
	if s == "" {
		if i := strings.IndexByte(message, '\n'); i >= 0 {
			s = strings.TrimSpace(message[:i])
		} else {
			s = strings.TrimSpace(message)
		}
	}
	m.stats.runSummary = truncRunes(s, statusSummaryMax)
}

// truncRunes 按 rune 数截断（stTrimShort 按字节截断，可能切断多字节字符）。
func truncRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// statusOneLine 返回当前运行状态一行（随任务变化：progress / tool / state）。
func (m *model) statusOneLine() string {
	state := strings.TrimSpace(m.stats.state)
	if m.streaming {
		if state != "" && state != "ready" {
			return truncRunes(state, 24)
		}
		if lt := strings.TrimSpace(m.stats.lastTool); lt != "" {
			return lt
		}
		return "流式"
	}
	switch state {
	case "", "ready":
		return "就绪"
	case "thinking":
		return "思考"
	default:
		return truncRunes(state, 24)
	}
}

// runSummaryLine 侧栏概要一行；无 log 摘要时回退到轮次信息。
func (m *model) runSummaryLine() string {
	if s := strings.TrimSpace(m.stats.runSummary); s != "" {
		return s
	}
	if m.stats.round > 0 {
		return fmt.Sprintf("round %d", m.stats.round)
	}
	if m.streaming {
		return "对话进行中"
	}
	return ""
}

// openStatusView 打开「运行详情」overlay：展示最近 log / progress / tool 细节。
func (m *model) openStatusView() (tea.Model, tea.Cmd) {
	if len(m.stats.runDetails) == 0 && strings.TrimSpace(m.stats.runSummary) == "" {
		return m, nil
	}
	var b strings.Builder
	b.WriteString("运行状态: " + m.statusOneLine() + "\n")
	if s := strings.TrimSpace(m.stats.runSummary); s != "" {
		b.WriteString("概要: " + s + "\n")
	}
	if m.stats.round > 0 {
		fmt.Fprintf(&b, "round %d · turns %d · tools %d\n", m.stats.round, m.stats.turns, m.stats.tools)
	}
	if len(m.stats.runDetails) > 0 {
		b.WriteString("\n运行细节\n")
		b.WriteString(strings.Repeat("─", 32))
		b.WriteString("\n")
		b.WriteString(strings.Join(m.stats.runDetails, "\n"))
		b.WriteString("\n")
	} else {
		b.WriteString("\n（暂无运行细节）\n")
	}
	vp := viewport.New(64, 18)
	vp.SetContent(b.String())
	m.overlay = &overlayState{mode: overlayStatusView, statusVP: vp}
	return m, nil
}

// statusLineAtSidebarLine 判断侧栏某行是否为「状态」行（点击查看运行详情）。
func (m *model) statusLineAtSidebarLine(lineIdx int) bool {
	if lineIdx < 0 {
		return false
	}
	lines := strings.Split(m.sidebarText(), "\n")
	if lineIdx >= len(lines) {
		return false
	}
	line := strings.TrimSpace(lines[lineIdx])
	return strings.HasPrefix(line, "状态 ") || strings.HasPrefix(line, "状态:")
}

// renderStatusOverlay 渲染「运行详情」overlay。
func (m *model) renderStatusOverlay() string {
	if m.overlay == nil || m.overlay.mode != overlayStatusView {
		return ""
	}
	title := "运行详情"
	body := m.overlay.statusVP.View()
	help := styleDim.Render("↑↓/滚轮 滚动 · esc 关闭")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("39")).
		Padding(1, 2).
		Width(72).
		Render(title + "\n\n" + body + "\n" + help)
}
