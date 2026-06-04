package client

import tea "github.com/charmbracelet/bubbletea"

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
	chatH := m.vp.Height + inputLinesBorder
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

func chatScrollKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "pgup", "pgdown", "home", "end", "up", "down":
		return true
	default:
		return false
	}
}
