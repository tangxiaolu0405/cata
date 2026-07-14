package client

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type hoverPane int

const (
	hoverNone hoverPane = iota
	hoverChat
	hoverSidebar
	hoverInput
)

type paneLayout struct {
	mainW      int
	chatH      int // bordered chat block (viewport + border)
	inputH     int
	leftBodyH  int
	sidebarX   int
	sidebarW   int
}

func (m *model) paneLayout() paneLayout {
	side := 0
	if sidebarActive(m.width) {
		side = sidebarWidth
	}
	mainW := m.width - side - 2
	if mainW < minMainWidth {
		mainW = m.width - 2
		side = 0
	}
	inputH := m.inputLineCount() + inputLinesBorder + m.slashMenuLines()
	chatH := m.vp.Height + 2 // 主区圆角边框上下各 1 行
	leftBodyH := chatH + inputH
	return paneLayout{
		mainW:     mainW,
		chatH:     chatH,
		inputH:    inputH,
		leftBodyH: leftBodyH,
		sidebarX:  mainW + 2,
		sidebarW:  sidebarWidth,
	}
}

func (pl paneLayout) hit(x, y int) hoverPane {
	if x < 0 || y < 0 {
		return hoverNone
	}
	if pl.sidebarW > 0 && x >= pl.sidebarX && x < pl.sidebarX+pl.sidebarW && y < pl.leftBodyH {
		return hoverSidebar
	}
	if x < pl.mainW+2 {
		if y < pl.chatH {
			return hoverChat
		}
		if y < pl.chatH+pl.inputH {
			return hoverInput
		}
	}
	return hoverNone
}

func (m *model) leftBodyHeight() int {
	return m.paneLayout().leftBodyH
}

func (m *model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd, bool) {
	if m.overlay != nil {
		return m, nil, false
	}
	pl := m.paneLayout()
	m.hoverPane = pl.hit(msg.X, msg.Y)

	if !tea.MouseEvent(msg).IsWheel() {
		return m, nil, false
	}

	var cmd tea.Cmd
	switch m.hoverPane {
	case hoverChat:
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd, true
	case hoverSidebar:
		if sidebarActive(m.width) {
			m.sidebarVP, cmd = m.sidebarVP.Update(msg)
			return m, cmd, true
		}
	}
	// Wheel over input/footer: do not scroll chat or sidebar.
	return m, nil, true
}

// wrapChatLog 按视口宽度硬折行，使 viewport 行数与终端可见行一致（避免滚到底仍被 lipgloss 二次折行截断）。
func wrapChatLog(width int, text string) string {
	if text == "" {
		return text
	}
	if width < 1 {
		width = 1
	}
	rendered := lipgloss.NewStyle().Width(width).Render(text)
	lines := strings.Split(strings.TrimSuffix(rendered, "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// viewColumnHeight 在给定聊天区内高（不含边框）下，测算左列（聊天+输入+页脚）实际渲染行数。
func (m *model) viewColumnHeight(mainH int) int {
	saved := m.vp.Height
	m.vp.Height = mainH
	main := styleBorder.Width(m.vp.Width + 2).Render(m.vp.View())
	col := lipgloss.JoinVertical(lipgloss.Top, main, m.renderInputPane())
	total := lipgloss.JoinVertical(lipgloss.Left, col, m.footerView())
	h := lipgloss.Height(total)
	m.vp.Height = saved
	return h
}

// setChatContent 将原始 log 折行后写入 viewport，并尽量保持滚动位置。
func (m *model) setChatContent(scrollToBottom bool) {
	w := m.vp.Width
	if w < 1 {
		w = 80
	}
	atBottom := scrollToBottom || m.vp.AtBottom()
	pct := m.vp.ScrollPercent()
	m.vp.SetContent(wrapChatLog(w, m.log))
	if atBottom {
		m.vp.GotoBottom()
		return
	}
	maxOff := m.vp.TotalLineCount() - m.vp.Height
	if maxOff < 0 {
		maxOff = 0
	}
	m.vp.SetYOffset(int(pct * float64(maxOff)))
}

func chatScrollKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "pgup", "pgdown", "home", "end", "up", "down":
		return true
	default:
		return false
	}
}
