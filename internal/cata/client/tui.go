package client

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
)

const (
	sidebarWidth         = 42
	sidebarActivateWidth = 96 // 主区 + 侧栏最小总宽（见 agents.md）
	minMainWidth         = 48
	inputMinLines    = 3
	inputMaxLines    = 8
	inputLinesBorder = 2
	// 粘贴多行时终端常在行间注入 Enter；短延迟可区分「粘贴」与「 intentional 发送」。
	composeSendDelay = 80 * time.Millisecond
)

var (
	styleDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	styleUser    = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	styleTool    = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	styleBorder  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238"))
	styleSidebar      = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(lipgloss.Color("238")).Padding(0, 1)
	styleSidebarLabel = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Bold(true)
)

type overlayMode int

const (
	overlayNone overlayMode = iota
	overlayConfirm
	overlayChoice
	overlaySubagentPick
	overlaySubagentView
)

type overlayState struct {
	mode      overlayMode
	list      list.Model
	confirmID string
	choiceID  string
	multi     bool
	subagentID string
	subagentVP viewport.Model
}

type paneStats struct {
	round, turns, tools int
	lastTool, state     string
	wsID, outputCwd     string
	focusPath, mode     string
	sessionTok          int
	contextEst            int
	evolveOn              bool
	evolveSec             int
	evolveLast            string
	chatModel             string
	promptProfile         string
	subagentRunning       int
	subagentMax           int
}

type pickItem struct {
	id, title, desc string
}

func (i pickItem) FilterValue() string { return i.title }
func (i pickItem) Title() string       { return i.title }
func (i pickItem) Description() string { return i.desc }

type model struct {
	sess   *session
	cwd    string
	width  int
	height int

	vp     viewport.Model
	sidebarVP viewport.Model
	input  textarea.Model
	log    string
	lastIn string

	streaming bool
	overlay   *overlayState

	stats    paneStats
	runtime  brain.RuntimeEnv
	quitting bool
	errLine  string

	composeSendSeq uint64 // 当前挂起的 Enter 发送代号，0 表示无
	slashList      *list.Model
	hoverPane      hoverPane // 鼠标所在区域，决定滚轮/翻页滚动目标
	subagents      []subagentRecord
}

type streamTickMsg struct {
	ev streamEvent
}

// composeReleaseMsg：Enter 防抖结束后尝试发送。
type composeReleaseMsg struct {
	seq uint64
}

func newChatTextarea() textarea.Model {
	ta := textarea.New()
	ta.Placeholder = "Message…  (/help)"
	ta.CharLimit = 0
	ta.ShowLineNumbers = false
	ta.SetHeight(inputMinLines)
	ta.Focus()
	return ta
}

func newModel(s *session, cwd string) model {
	ti := newChatTextarea()

	welcome := styleDim.Render("Type a message. enter send · double-enter newline · ctrl+v paste") + "\n"
	vp := viewport.New(80, 20)

	m := model{
		sess:      s,
		cwd:       cwd,
		vp:        vp,
		sidebarVP: newSidebarViewport(),
		input:     ti,
		log:     welcome,
		stats:   paneStats{state: "ready", outputCwd: cwd},
		runtime: brain.DetectRuntimeEnvFromProcess(),
		width:   100,
		height:  24,
	}
	if w := brain.Active(); w != nil {
		m.stats.wsID = w.ID
		m.stats.focusPath = w.RootPath
		m.stats.mode = w.ActiveMode
	}
	m.loadEvolve()
	m.setChatContent(true)
	return m
}

func RunChat(dirs []string) {
	if err := config.InitBrainPath(); err != nil {
		fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	if len(dirs) > 0 {
		cwd = dirs[0]
		if err := os.Chdir(cwd); err != nil {
			fatal(err)
		}
	}
	release, err := AcquireOutputLock(cwd)
	if err != nil {
		fatal(err)
	}
	defer release()

	if err := EnsureServer(); err != nil {
		fatal(err)
	}
	s, err := dial()
	if err != nil {
		fatal(err)
	}
	defer s.conn.Close()

	bindStats(cwd)
	m := newModel(s, cwd)
	p := tea.NewProgram(&m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fatal(err)
	}
}

func (m *model) Init() tea.Cmd {
	return m.input.Focus()
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.overlay != nil {
			return m.updateOverlayKey(msg)
		}
		if m.handleSidebarScroll(msg) {
			return m, nil
		}
		if m.streaming {
			switch msg.String() {
			case "ctrl+c":
				_ = m.sess.write(req{Command: "chat_cancel"})
				return m, nil
			}
			if m.hoverPane == hoverChat && chatScrollKey(msg) {
				m.vp, _ = m.vp.Update(msg)
				return m, nil
			}
			return m, nil
		}
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if msg.String() == "d" && len(m.subagents) > 0 {
			return m.openSubagentPicker()
		}
		// 粘贴（bracketed paste）整段进编辑区，不触发发送。
		if msg.Paste {
			break
		}
		if handled, cmd := m.updateSlashKeys(msg); handled {
			return m, cmd
		}
		if msg.Type == tea.KeyEnter && msg.String() == "enter" {
			if m.composeSendSeq != 0 {
				// 粘贴/多行：连续 Enter → 换行，取消待发发送
				m.composeSendSeq = 0
				break
			}
			m.composeSendSeq++
			seq := m.composeSendSeq
			return m, tea.Tick(composeSendDelay, func(time.Time) tea.Msg {
				return composeReleaseMsg{seq: seq}
			})
		}

	case composeReleaseMsg:
		return m.tryComposeSend(msg.seq)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.syncInputSize()
		return m, nil

	case streamTickMsg:
		return m.handleStream(msg.ev)

	case tea.MouseMsg:
		if nm, cmd, handled := m.handleSidebarClick(msg); handled {
			return nm, cmd
		}
		if nm, cmd, handled := m.handleMouse(msg); handled {
			return nm, cmd
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.syncInputSize()
	listCmd := m.syncSlashList()
	if keyMsg, ok := msg.(tea.KeyMsg); ok && m.hoverPane == hoverChat && chatScrollKey(keyMsg) {
		m.vp, _ = m.vp.Update(msg)
	}
	return m, tea.Batch(cmd, listCmd)
}

func (m *model) mainInputWidth() int {
	side := 0
	if sidebarActive(m.width) {
		side = sidebarWidth
	}
	mainW := m.width - side - 2
	if mainW < minMainWidth {
		mainW = m.width - 2
	}
	return mainW
}

func (m *model) slashMatches() []cmdDef {
	if m.streaming || m.overlay != nil {
		return nil
	}
	first, _ := firstInputLine(m.input.Value())
	if !strings.HasPrefix(first, "/") {
		return nil
	}
	return matchSlashCmds(first[1:])
}

// syncSlashList 用 bubbles/list 渲染 / 命令菜单（Charm 生态标准组件）。
func (m *model) syncSlashList() tea.Cmd {
	matches := m.slashMatches()
	if len(matches) == 0 {
		m.slashList = nil
		return nil
	}
	items := make([]list.Item, 0, len(matches))
	for _, c := range matches {
		items = append(items, pickItem{id: c.Name, title: "/" + c.Name, desc: c.Desc})
	}
	w := m.mainInputWidth()
	if w < 20 {
		w = 20
	}
	h := min(8, len(matches)+1)
	if m.slashList == nil {
		l := list.New(items, newSlashCmdDelegate(), w, h)
		l.SetShowTitle(false)
		l.SetShowFilter(false)
		l.SetFilteringEnabled(false)
		l.SetShowHelp(false)
		l.SetShowStatusBar(false)
		m.slashList = &l
		return nil
	}
	return m.slashList.SetItems(items)
}

// updateSlashKeys 在菜单打开时处理 ↑↓/Tab/Esc；完整命令名时 Enter 交给发送逻辑。
func (m *model) updateSlashKeys(msg tea.KeyMsg) (bool, tea.Cmd) {
	if m.slashList == nil {
		return false, nil
	}
	// 已输入完整 /exit、/help 等：Enter 发送/执行，不要只补空格。
	if msg.Type == tea.KeyEnter && msg.String() == "enter" && slashLineComplete(m.input.Value()) {
		return false, nil
	}
	switch msg.String() {
	case "tab":
		m.input.SetValue(tabCompleteSlash(m.input.Value()))
		m.syncInputSize()
		return true, m.syncSlashList()
	case "esc":
		m.composeSendSeq = 0
		m.input.SetValue("")
		m.slashList = nil
		m.syncInputSize()
		return true, nil
	case "enter":
		if it, ok := m.slashList.SelectedItem().(pickItem); ok {
			m.composeSendSeq = 0
			m.input.SetValue("/" + it.id + " ")
			m.syncInputSize()
			return true, m.syncSlashList()
		}
		return true, nil
	case "up", "down":
		var cmd tea.Cmd
		*m.slashList, cmd = m.slashList.Update(msg)
		return true, cmd
	}
	return false, nil
}

func (m *model) slashMenuLines() int {
	if m.slashList == nil {
		return 0
	}
	// 分隔线 1 行 + 列表
	h := lipgloss.Height(m.slashList.View())
	if h < 1 {
		return 1
	}
	return 1 + h
}

// renderInputPane 输入区：编辑行 +（可选）框内 / 命令列表。
func (m *model) renderInputPane() string {
	borderW := m.vp.Width + 2
	parts := []string{m.input.View()}
	if m.slashList != nil {
		sepW := m.mainInputWidth()
		if sepW > 4 {
			sepW = min(sepW-2, 32)
		}
		sep := styleDim.Render("  " + strings.Repeat("─", sepW))
		parts = append(parts, sep, m.slashList.View())
	}
	inner := lipgloss.JoinVertical(lipgloss.Left, parts...)
	return styleBorder.Width(borderW).Render(inner)
}

func (m *model) tryComposeSend(seq uint64) (tea.Model, tea.Cmd) {
	if m.composeSendSeq != seq {
		return m, nil
	}
	m.composeSendSeq = 0
	m.slashList = nil
	line := strings.TrimSpace(m.input.Value())
	m.input.SetValue("")
	m.syncInputSize()
	if line == "" {
		return m, nil
	}
	return m.handleInput(line)
}

func (m *model) handleInput(line string) (tea.Model, tea.Cmd) {
	trimmed := strings.TrimSpace(line)
	if name, ok := matchSlash(trimmed); ok && !strings.Contains(trimmed, "\n") {
		switch name {
		case "exit", "quit", "q":
			return m, tea.Quit
		case "clear", "reset":
			r, err := m.sess.call(req{Command: "chat_reset"})
			if err != nil {
				m.appendLog(styleErr.Render("! "+err.Error()) + "\n", true)
				return m, nil
			}
			if !r.Success {
				m.appendLog(styleErr.Render("! "+r.Message)+"\n", true)
				return m, nil
			}
			m.stats.turns, m.stats.round, m.stats.tools = 0, 0, 0
			m.stats.sessionTok = 0
			m.stats.state = "ready"
			m.stats.promptProfile = ""
			m.subagents = nil
			m.log = styleDim.Render("— session cleared") + "\n"
			m.setChatContent(true)
			m.lastIn = ""
			return m, nil
		case "cls":
			m.log = styleDim.Render("— view cleared") + "\n"
			m.setChatContent(true)
			return m, nil
		case "help":
			m.appendLog("/clear /cls /exit /status /retry /config\n", true)
			return m, nil
		case "status":
			m.refreshRuntime()
			m.appendLog(m.statusDump()+"\n", true)
			return m, nil
		case "config":
			m.appendLog(config.GetConfigPath()+"\n", true)
			return m, nil
		case "retry":
			if m.lastIn == "" {
				m.appendLog("nothing to retry\n", true)
				return m, nil
			}
			line = m.lastIn
		default:
			m.appendLog("unknown /"+name+"\n", true)
			return m, nil
		}
	}

	m.lastIn = line
	m.stats.turns++
	m.stats.state = "thinking"
	m.appendLog(styleUser.Render("you: ")+line+"\n\n", true)
	m.streaming = true
	m.input.Blur()

	outCwd, _ := os.Getwd()
	if m.cwd != "" {
		outCwd = m.cwd
	}
	rt := m.runtime
	if err := m.sess.write(req{Command: "chat", Text: line, Stream: true, Cwd: outCwd, Runtime: &rt}); err != nil {
		m.streaming = false
		m.input.Focus()
		m.appendLog(styleErr.Render("! "+err.Error())+"\n", true)
		return m, m.input.Focus()
	}
	return m, waitStream(m.sess)
}

func waitStream(s *session) tea.Cmd {
	return func() tea.Msg {
		return streamTickMsg{ev: readStreamEvent(s)}
	}
}

func (m *model) handleStream(ev streamEvent) (tea.Model, tea.Cmd) {
	if ev.kind == "io" {
		m.streaming = false
		m.input.Focus()
		m.appendLog(styleErr.Render("! "+ev.err.Error())+"\n", true)
		if connLost(ev.err) {
			m.appendLog(styleDim.Render("disconnected — try again\n"), true)
			if err := EnsureServer(); err != nil {
				m.appendLog(styleErr.Render("! server: "+err.Error())+"\n", true)
			} else if ns, e := dial(); e == nil {
				if old := m.sess; old != nil && old.conn != nil {
					_ = old.conn.Close()
				}
				m.sess = ns
				m.appendLog(styleDim.Render("reconnected\n"), true)
			}
		}
		return m, m.input.Focus()
	}
	if ev.kind == "skip" {
		return m, waitStream(m.sess)
	}

	switch ev.kind {
	case "token":
		c := str(ev.raw["content"])
		if c != "" {
			m.appendLog(c, false)
		}
	case "stats":
		m.applyStats(ev.raw)
		m.syncSidebarViewport()
	case "progress":
		m.stats.state = str(ev.raw["message"])
	case "tool_start":
		if n := str(ev.raw["name"]); n != "" {
			m.stats.lastTool = n
			m.stats.state = n
			m.appendLog(styleTool.Render("\n▸ "+n)+"\n", true)
		}
	case "tool_result":
		if line := formatToolResultLine("tool_result", ev.raw); line != "" {
			m.appendLog(styleDim.Render(line)+"\n", true)
		}
	case "subagent_start", "subagent_queued", "subagent_progress", "subagent_tool", "subagent_done":
		m.handleSubagentStream(ev.kind, ev.raw)
		m.syncSidebarViewport()
	case "exec_confirm_required":
		return m.startConfirmOverlay(ev.raw)
	case "exec_denied":
		m.appendLog(styleDim.Render("— cancelled\n"), true)
	case "exec_done":
		m.sess.lastExecCmd = execLine(ev.raw)
		m.sess.lastExecCwd = str(ev.raw["cwd"])
		m.appendLog(formatToolResultLine("exec_done", ev.raw)+"\n", true)
	case "error":
		m.appendLog(styleErr.Render("! "+str(ev.raw["message"]))+"\n", true)
	case "user_choice":
		return m.startChoiceOverlay(ev.raw)
	case "done":
		if ev.raw["cancelled"] == true {
			m.appendLog(styleDim.Render("\n— stopped\n"), true)
		} else if ev.raw["success"] != true {
			m.appendLog(styleErr.Render("\n! chat failed\n"), true)
		} else {
			m.appendLog("\n", true)
		}
		m.stats.state = "ready"
		m.streaming = false
		m.input.Focus()
		return m, m.input.Focus()
	}

	if ev.done {
		m.streaming = false
		return m, m.input.Focus()
	}
	return m, waitStream(m.sess)
}

func (m *model) startConfirmOverlay(ev map[string]any) (tea.Model, tea.Cmd) {
	id := str(ev["confirm_id"])
	cmd := execLine(ev)
	cwd := str(ev["cwd"])
	m.sess.lastExecCmd, m.sess.lastExecCwd = cmd, cwd
	desc := cmd
	if len(desc) > 240 {
		desc = desc[:240] + "…"
	}
	items := []list.Item{
		pickItem{id: "run", title: "Run", desc: desc},
		pickItem{id: "cancel", title: "Cancel", desc: cwd},
	}
	l := list.New(items, list.NewDefaultDelegate(), 40, 8)
	l.SetShowTitle(false)
	l.SetFilteringEnabled(false)
	m.overlay = &overlayState{mode: overlayConfirm, list: l, confirmID: id}
	m.stats.state = "confirm run"
	m.appendLog(styleTool.Render("\n▸ run_command 待确认 (↑↓ 选 Run/Cancel，Enter 确认，Esc 取消)\n"), true)
	return m, nil
}

func (m *model) dismissOverlayWithCancel() {
	switch m.overlay.mode {
	case overlayConfirm:
		if m.overlay.confirmID != "" {
			_ = m.sess.write(req{Command: "exec_confirm", ConfirmID: m.overlay.confirmID, Approved: false})
			m.appendLog(styleDim.Render("— command cancelled (Esc)\n"), true)
		}
	case overlayChoice:
		if m.overlay.choiceID != "" {
			_ = m.sess.writeChoice(m.overlay.choiceID, nil)
			m.appendLog(styleDim.Render("— choice cancelled (Esc)\n"), true)
		}
	}
	m.overlay = nil
}

func (m *model) startChoiceOverlay(ev map[string]any) (tea.Model, tea.Cmd) {
	id := str(ev["id"])
	prompt := str(ev["prompt"])
	multi, _ := ev["multi"].(bool)
	var items []list.Item
	for _, o := range parseChoiceOptions(ev["options"]) {
		items = append(items, pickItem{id: o.id, title: o.label, desc: o.desc})
	}
	if len(items) < 2 {
		m.appendLog(styleErr.Render("! invalid user_choice\n"), true)
		return m, waitStream(m.sess)
	}
	l := list.New(items, list.NewDefaultDelegate(), 50, min(12, len(items)+2))
	l.SetShowTitle(true)
	l.Title = prompt
	l.SetFilteringEnabled(false)
	m.overlay = &overlayState{mode: overlayChoice, list: l, choiceID: id, multi: multi}
	return m, nil
}

func (m *model) updateOverlayKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		if m.overlay.mode == overlaySubagentView {
			m.overlay = nil
			if m.streaming {
				return m, waitStream(m.sess)
			}
			return m, nil
		}
		if m.streaming && (m.overlay.mode == overlayConfirm || m.overlay.mode == overlayChoice) {
			m.dismissOverlayWithCancel()
			return m, waitStream(m.sess)
		}
		m.overlay = nil
		if m.streaming {
			return m, waitStream(m.sess)
		}
		return m, nil
	case "enter":
		if m.overlay.mode == overlaySubagentView {
			return m, nil
		}
		it, ok := m.overlay.list.SelectedItem().(pickItem)
		if !ok {
			return m, nil
		}
		switch m.overlay.mode {
		case overlayConfirm:
			okRun := it.id == "run"
			_ = m.sess.write(req{Command: "exec_confirm", ConfirmID: m.overlay.confirmID, Approved: okRun})
			if !okRun {
				m.appendLog(styleDim.Render("— command cancelled\n"), true)
			}
			m.overlay = nil
			return m, waitStream(m.sess)
		case overlayChoice:
			var sel []string
			if m.overlay.multi {
				sel = []string{it.id}
			} else {
				sel = []string{it.id}
			}
			_ = m.sess.writeChoice(m.overlay.choiceID, sel)
			m.overlay = nil
			return m, waitStream(m.sess)
		case overlaySubagentPick:
			return m.openSubagentView(it.id)
		}
	}
	if m.overlay.mode == overlaySubagentView {
		var cmd tea.Cmd
		m.overlay.subagentVP, cmd = m.overlay.subagentVP.Update(key)
		return m, cmd
	}
	var cmd tea.Cmd
	m.overlay.list, cmd = m.overlay.list.Update(key)
	return m, cmd
}

func (m *model) appendLog(s string, scroll bool) {
	m.log += s
	m.setChatContent(scroll)
}

func (m *model) inputLineCount() int {
	n := 1
	if v := m.input.Value(); v != "" {
		n = strings.Count(v, "\n") + 1
	}
	if n < inputMinLines {
		n = inputMinLines
	}
	if n > inputMaxLines {
		n = inputMaxLines
	}
	return n
}

func (m *model) syncInputSize() {
	h := m.inputLineCount()
	m.input.SetHeight(h)
	if m.width <= 0 {
		return
	}
	side := 0
	if sidebarActive(m.width) {
		side = sidebarWidth
	}
	mainW := m.width - side - 2
	if mainW < minMainWidth {
		mainW = m.width - 2
	}
	m.input.SetWidth(mainW)
	if m.height > 0 {
		m.layoutViewports()
	}
}

// layoutViewports sizes the chat viewport to fit the terminal without clipping the top row.
func (m *model) layoutViewports() {
	side := 0
	if sidebarActive(m.width) {
		side = sidebarWidth
	}
	mainW := m.width - side - 2
	if mainW < minMainWidth {
		mainW = m.width - 2
		side = 0
	}
	widthChanged := m.vp.Width != mainW
	wasAtBottom := m.vp.AtBottom() || m.vp.PastBottom()
	m.vp.Width = mainW
	if m.input.Width() != mainW {
		m.input.SetWidth(mainW)
	}

	// 用 lipgloss 实测左列总高，避免手算行数偏差导致整页底部被终端裁掉（常见少 1～2 行）。
	mainH := m.height - lipgloss.Height(m.renderInputPane()) - lipgloss.Height(m.footerView()) - 2
	if mainH < 4 {
		mainH = 4
	}
	for mainH >= 4 && m.viewColumnHeight(mainH) > m.height {
		mainH--
	}

	m.vp.Height = mainH
	if m.log != "" {
		if widthChanged {
			m.setChatContent(false)
		} else if wasAtBottom {
			m.vp.GotoBottom()
		}
	}
	m.syncSidebarViewport()
}

func (m *model) footerView() string {
	if m.streaming {
		return styleDim.Render("streaming… ctrl+c cancel round")
	}
	if m.slashList != nil {
		return styleDim.Render("↑↓ select · enter run (or apply) · tab complete · esc clear")
	}
	if sidebarActive(m.width) {
		return styleDim.Render("滚轮切换区 · d 子任务 · /status 详情 · ctrl+c")
	}
	return styleDim.Render("enter send · double-enter newline · ctrl+v paste · ctrl+c quit · wheel scrolls pane under cursor")
}

func (m *model) View() string {
	if m.width == 0 {
		return "Loading…"
	}
	m.layoutViewports()
	main := styleBorder.Width(m.vp.Width + 2).Render(m.vp.View())
	in := m.renderInputPane()
	body := lipgloss.JoinVertical(lipgloss.Top, main, in)
	if sidebarActive(m.width) {
		bodyH := m.leftBodyHeight()
		sb := styleSidebar.
			Width(sidebarWidth).
			Height(bodyH).
			MaxHeight(bodyH).
			Render(m.sidebarVP.View())
		body = lipgloss.JoinHorizontal(lipgloss.Top, body, sb)
	}

	foot := m.footerView()
	if m.overlay != nil {
		overlay := m.renderSubagentOverlay()
		if overlay == "" && (m.overlay.mode == overlayConfirm || m.overlay.mode == overlayChoice || m.overlay.mode == overlaySubagentPick) {
			overlay = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("205")).
				Padding(1, 2).
				Render(m.overlay.list.View())
		}
		if overlay != "" {
			body = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, overlay)
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, body, foot)
}

func (m *model) refreshRuntime() {
	m.runtime = brain.DetectRuntimeEnvFromProcess()
}

func matchSlash(line string) (string, bool) {
	if !strings.HasPrefix(line, "/") {
		return "", false
	}
	cmd := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "/")))
	if cmd == "" {
		return "", true
	}
	for _, c := range []struct{ n string; a []string }{
		{"exit", []string{"quit", "q"}},
		{"clear", []string{"reset"}},
		{"help", nil}, {"status", nil}, {"retry", nil}, {"config", nil}, {"cls", nil},
	} {
		if cmd == c.n {
			return c.n, true
		}
		for _, a := range c.a {
			if cmd == a {
				return c.n, true
			}
		}
	}
	return cmd, true
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func connLost(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return !ne.Timeout()
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "closed") || strings.Contains(msg, "broken") || strings.Contains(msg, "reset")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
