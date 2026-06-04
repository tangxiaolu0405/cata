package client

import (
	"os"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

func sidebarActive(width int) bool {
	if os.Getenv("CATA_NO_SIDEBAR") != "" {
		return false
	}
	return width >= sidebarActivateWidth
}

// syncSidebarViewport 侧栏高度与左侧（chat + 输入区）对齐，避免 JoinHorizontal 时错位。
func (m *model) syncSidebarViewport() {
	if !sidebarActive(m.width) {
		return
	}
	h := m.leftBodyHeight()
	if h < 4 {
		h = 4
	}
	w := sidebarInnerWidth()
	if m.sidebarVP.Width != w || m.sidebarVP.Height != h {
		m.sidebarVP.Width = w
		m.sidebarVP.Height = h
	}
	m.sidebarVP.SetContent(m.sidebarText())
}

func (m *model) handleSidebarScroll(msg tea.KeyMsg) bool {
	if !sidebarActive(m.width) {
		return false
	}
	s := msg.String()
	switch {
	// Windows/Git Bash：ctrl+[ 常为 esc 键码，仅匹配字符串；] 可用 KeyCtrlCloseBracket
	case s == "ctrl+[":
		m.sidebarVP.LineUp(1)
		return true
	case s == "ctrl+]", msg.Type == tea.KeyCtrlCloseBracket:
		m.sidebarVP.LineDown(1)
		return true
	case s == "ctrl+up", msg.Type == tea.KeyCtrlUp:
		m.sidebarVP.LineUp(1)
		return true
	case s == "ctrl+down", msg.Type == tea.KeyCtrlDown:
		m.sidebarVP.LineDown(1)
		return true
	case s == "alt+up":
		m.sidebarVP.LineUp(1)
		return true
	case s == "alt+down":
		m.sidebarVP.LineDown(1)
		return true
	case s == "ctrl+home", msg.Type == tea.KeyCtrlHome:
		m.sidebarVP.GotoTop()
		return true
	case s == "ctrl+end", msg.Type == tea.KeyCtrlEnd:
		m.sidebarVP.GotoBottom()
		return true
	case s == "alt+home":
		m.sidebarVP.GotoTop()
		return true
	case s == "alt+end":
		m.sidebarVP.GotoBottom()
		return true
	}
	return false
}

func newSidebarViewport() viewport.Model {
	vp := viewport.New(sidebarInnerWidth(), 12)
	vp.YPosition = 0
	return vp
}
