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
	"cata/internal/cata/link"
)

const (
	sidebarWidth         = 42
	sidebarActivateWidth = 96 // 主区 + 侧栏最小总宽（见 agents.md）
	minMainWidth         = 48
	inputMinLines        = 3
	inputMaxLines        = 8
	inputLinesBorder     = 2
	// 粘贴多行时终端常在行间注入 Enter；短延迟可区分「粘贴」与「 intentional 发送」。
	composeSendDelay = 80 * time.Millisecond
)

var (
	styleDim          = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	styleUser         = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	styleTool         = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleErr          = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	styleBorder       = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238"))
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
	overlayStatusView
)

type overlayState struct {
	mode       overlayMode
	list       list.Model
	confirmID  string
	choiceID   string
	multi      bool
	subagentID string
	subagentVP viewport.Model
	statusVP   viewport.Model
}

type paneStats struct {
	round, turns, tools int
	lastTool, state     string
	wsID, outputCwd     string
	focusPath, mode     string
	projectCata         string
	cataHome            string
	sessionTok          int
	contextEst          int
	evolveOn            bool
	evolveSec           int
	evolveLast          string
	chatModel           string
	promptProfile       string
	subagentRunning     int
	subagentMax         int

	// runSummary 一行概要：最近一条 log 事件摘要（服务端诊断等），不刷主区。
	runSummary string
	// runDetails 运行细节环形缓冲：log / progress / tool_start 等，供点击状态查看。
	runDetails []string
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
	wsID   string // per-ws agent 绑定的工作空间 id
	width  int
	height int

	vp        viewport.Model
	sidebarVP viewport.Model
	input     textarea.Model
	log       string
	lastIn    string

	streaming bool
	overlay   *overlayState
	// cancelRequested：流式中已发过一次 chat_cancel；再按 ctrl+c 则强制退出。
	cancelRequested bool

	stats    paneStats
	runtime  brain.RuntimeEnv
	quitting bool
	errLine  string

	// displayMode：""=auto（按事件 level）/ quiet（隐藏工具输出）/ verbose（完整输出）。
	displayMode string
	// showThinking：--show-thinking 时展示服务端 thinking 事件（模型推理）。
	showThinking bool
	// thinkingActive：本段 thinking 块已开启，首个 token 到达前保持；用于块边界处理。
	thinkingActive bool

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

func newModel(s *session, cwd string, ws *brain.Workspace) model {
	ti := newChatTextarea()

	welcome := styleDim.Render("Type a message. enter send · double-enter newline · ctrl+v paste") + "\n"
	vp := viewport.New(80, 20)

	m := model{
		sess:      s,
		cwd:       cwd,
		vp:        vp,
		sidebarVP: newSidebarViewport(),
		input:     ti,
		log:       welcome,
		stats:     paneStats{state: "ready", outputCwd: cwd},
		runtime:   brain.DetectRuntimeEnvFromProcess(),
		width:     100,
		height:    24,
	}
	if ws != nil {
		m.stats.wsID = ws.ID
		m.stats.focusPath = ws.RootPath
		m.stats.projectCata = ws.ProjectCataRoot()
		m.stats.cataHome = brain.CataHome()
		m.stats.mode = ws.ActiveMode
	}
	m.loadEvolve()
	m.setChatContent(true)
	return m
}

func RunChat(opts ChatOptions) {
	if err := config.InitBrainPath(); err != nil {
		fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	if d := opts.firstDir(); d != "" {
		cwd = d
		if err := os.Chdir(cwd); err != nil {
			fatal(err)
		}
	}
	release, err := AcquireOutputLock(cwd)
	if err != nil {
		fatal(err)
	}
	defer release()

	// 扁平化：一个工作空间 = 一个 agent 进程 = 一个 LLM loop（per-ws socket）。
	// 本地未注册工作空间按需拉起、空闲回收；注册（cata link add）的常驻。
	ws, err := brain.ResolveWorkspace(cwd)
	if err != nil {
		fatal(err)
	}
	if err := link.EnsureAgent(ws.ID); err != nil {
		fatal(err)
	}
	s, err := dialAgent(ws.ID)
	if err != nil {
		fatal(err)
	}
	defer s.conn.Close()

	m := newModel(s, cwd, ws)
	m.wsID = ws.ID
	m.displayMode = opts.displayMode()
	m.showThinking = opts.ShowThinking
	p := tea.NewProgram(&m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fatal(err)
	}
}

// reconnect 断线后重连（重拨 per-ws agent socket）。
func (m *model) reconnect() error {
	if err := link.EnsureAgent(m.wsID); err != nil {
		return err
	}
	ns, err := dialAgent(m.wsID)
	if err != nil {
		return err
	}
	m.swapSession(ns)
	return nil
}

func (m *model) swapSession(ns *session) {
	if old := m.sess; old != nil && old.conn != nil {
		_ = old.conn.Close()
	}
	m.sess = ns
}

func (m *model) Init() tea.Cmd {
	return m.input.Focus()
}

func isQuitKey(msg tea.KeyMsg) bool {
	// macOS：终端中断是 Ctrl+C；Cmd+C 由终端做复制，应用收不到。
	return msg.Type == tea.KeyCtrlC || msg.String() == "ctrl+c"
}

func (m *model) handleQuitKey() (tea.Model, tea.Cmd) {
	if m.streaming {
		if m.cancelRequested {
			m.quitting = true
			m.appendLog(styledLogLine(styleDim, "\n— quit"), true)
			return m, tea.Quit
		}
		m.cancelRequested = true
		if m.overlay != nil {
			m.dismissOverlayWithCancel()
			m.overlay = nil
		}
		_ = m.sess.write(req{Command: "chat_cancel"})
		m.appendLog(styledLogLine(styleDim, "\n— cancel requested (ctrl+c again to quit)"), true)
		m.stats.state = "cancelling"
		return m, waitStream(m.sess)
	}
	if m.overlay != nil {
		m.dismissOverlayWithCancel()
		m.overlay = nil
	}
	m.quitting = true
	return m, tea.Quit
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// 先于 overlay / streaming 吞键：否则确认框或卡死流式时 ctrl+c 无法退出。
		if isQuitKey(msg) {
			return m.handleQuitKey()
		}
		if m.overlay != nil {
			return m.updateOverlayKey(msg)
		}
		if m.handleSidebarScroll(msg) {
			return m, nil
		}
		if m.streaming {
			if m.hoverPane == hoverChat && chatScrollKey(msg) {
				m.vp, _ = m.vp.Update(msg)
				return m, nil
			}
			return m, nil
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

func waitStream(s *session) tea.Cmd {
	return func() tea.Msg {
		return streamTickMsg{ev: readStreamEvent(s)}
	}
}

func (m *model) handleStream(ev streamEvent) (tea.Model, tea.Cmd) {
	if ev.kind == "io" {
		m.streaming = false
		m.cancelRequested = false
		m.input.Focus()
		m.closeThinking()
		m.appendLog(styleErr.Render("! "+ev.err.Error())+"\n", true)
		if connLost(ev.err) {
			m.appendLog(styledLogLine(styleDim, "disconnected — try again"), true)
			if err := m.reconnect(); err != nil {
				m.appendLog(styleErr.Render("! server: "+err.Error())+"\n", true)
			} else {
				m.appendLog(styledLogLine(styleDim, "reconnected"), true)
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
			m.closeThinking()
			m.appendLog(c, false)
		}
	case "thinking":
		c := str(ev.raw["content"])
		if c == "" || !m.showThinking {
			break
		}
		if !m.thinkingActive {
			m.thinkingActive = true
			m.appendLog(styledLogLine(styleDim, "\n┈ 思考中 ┈"), true)
		}
		m.appendLog(c, false)
	case "stats":
		m.applyStats(ev.raw)
		m.syncSidebarViewport()
	case "progress":
		m.closeThinking()
		m.stats.state = str(ev.raw["message"])
		m.appendRunDetail("• " + str(ev.raw["message"]))
		m.syncSidebarViewport()
	case "log":
		// 服务端诊断日志（如首次消息诊断）：全文进 cata-server.log，主区不再刷屏；
		// 侧栏只保留一行概要，完整内容进运行细节，点击「状态」可查看。
		msg := str(ev.raw["message"])
		if msg != "" {
			m.setRunSummary(str(ev.raw["summary"]), msg)
			m.appendRunDetail(msg)
			m.syncSidebarViewport()
		}
	case "tool_start":
		m.closeThinking()
		if n := str(ev.raw["name"]); n != "" {
			m.stats.lastTool = n
			m.stats.state = n
			m.appendRunDetail("▸ " + n)
			if m.displayMode == "quiet" {
				break
			}
			// silent 级工具（read/list）在 auto 模式下只进侧栏状态，不刷主区。
			if m.displayMode == "" && str(ev.raw["level"]) == "silent" {
				break
			}
			m.appendLog(styleTool.Render("\n▸ "+n)+"\n", true)
		}
	case "tool_result":
		if m.displayMode == "quiet" {
			break
		}
		if line := formatToolResultLine("tool_result", ev.raw, m.displayMode); line != "" {
			m.appendLog(styleDim.Render(line)+"\n", true)
		}
	case "subagent_start", "subagent_queued", "subagent_progress", "subagent_tool", "subagent_done":
		m.handleSubagentStream(ev.kind, ev.raw)
		m.syncSidebarViewport()
	case "exec_confirm_required":
		return m.startConfirmOverlay(ev.raw)
	case "exec_denied":
		m.appendLog(styledLogLine(styleDim, "— cancelled"), true)
	case "exec_done":
		m.sess.lastExecCmd = execLine(ev.raw)
		m.sess.lastExecCwd = str(ev.raw["cwd"])
		if m.displayMode != "quiet" {
			m.appendLog(formatToolResultLine("exec_done", ev.raw, m.displayMode)+"\n", true)
		}
	case "error":
		m.closeThinking()
		m.appendLog(styleErr.Render("! "+str(ev.raw["message"]))+"\n", true)
		m.appendRunDetail("! " + str(ev.raw["message"]))
		m.syncSidebarViewport()
	case "user_choice":
		return m.startChoiceOverlay(ev.raw)
	case "done":
		m.closeThinking()
		if ev.raw["cancelled"] == true {
			m.appendLog(styledLogLine(styleDim, "\n— stopped"), true)
			m.appendRunDetail("— stopped")
		} else if ev.raw["success"] != true {
			m.appendLog(styledLogLine(styleErr, "\n! chat failed"), true)
			m.appendRunDetail("! chat failed")
		} else {
			m.appendLog("\n", true)
			m.appendRunDetail("✓ done")
		}
		m.stats.state = "ready"
		m.streaming = false
		m.cancelRequested = false
		m.input.Focus()
		m.syncSidebarViewport()
		return m, m.input.Focus()
	}

	if ev.done {
		m.streaming = false
		m.cancelRequested = false
		return m, m.input.Focus()
	}
	return m, waitStream(m.sess)
}

func (m *model) startConfirmOverlay(ev map[string]any) (tea.Model, tea.Cmd) {
	id := str(ev["confirm_id"])
	cmd := execLine(ev)
	cwd := str(ev["cwd"])
	m.sess.lastExecCmd, m.sess.lastExecCwd = cmd, cwd
	title := str(ev["title"])
	if title == "" {
		title = "run_command 待确认 (↑↓ 选 Run/Cancel，Enter 确认，Esc 取消)"
	}
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
	m.appendLog(styledLogLine(styleTool, "\n▸ "+title), true)
	return m, nil
}

func (m *model) dismissOverlayWithCancel() {
	switch m.overlay.mode {
	case overlayConfirm:
		if m.overlay.confirmID != "" {
			_ = m.sess.write(req{Command: "exec_confirm", ConfirmID: m.overlay.confirmID, Approved: false})
			m.appendLog(styledLogLine(styleDim, "— command cancelled (Esc)"), true)
		}
	case overlayChoice:
		if m.overlay.choiceID != "" {
			_ = m.sess.writeChoice(m.overlay.choiceID, nil)
			m.appendLog(styledLogLine(styleDim, "— choice cancelled (Esc)"), true)
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
		m.appendLog(styledLogLine(styleErr, "! invalid user_choice"), true)
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
		if m.overlay.mode == overlaySubagentView || m.overlay.mode == overlayStatusView {
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
		if m.overlay.mode == overlaySubagentView || m.overlay.mode == overlayStatusView {
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
				m.appendLog(styledLogLine(styleDim, "— command cancelled"), true)
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
	if m.overlay.mode == overlaySubagentView || m.overlay.mode == overlayStatusView {
		var cmd tea.Cmd
		vp := &m.overlay.subagentVP
		if m.overlay.mode == overlayStatusView {
			vp = &m.overlay.statusVP
		}
		*vp, cmd = vp.Update(key)
		return m, cmd
	}
	var cmd tea.Cmd
	m.overlay.list, cmd = m.overlay.list.Update(key)
	return m, cmd
}

// closeThinking 结束当前 thinking 块（若已开启）：补一个换行避免与正文粘连。
func (m *model) closeThinking() {
	if m.thinkingActive {
		m.appendLog("\n", false)
		m.thinkingActive = false
	}
}

// styledLogLine 渲染一行日志文本，并把行尾换行放在样式之外。
// lipgloss 的 Render 会把多行文本的每一行补齐到最宽行；若文本以 \n 结尾，
// 末尾空行会被补成空格，这些空格会与后续追加的内容合并，导致 TUI 输出整体右移。
// 把 \n 移到样式外即可避免该问题。
func styledLogLine(st lipgloss.Style, text string) string {
	return st.Render(strings.TrimRight(text, "\n")) + "\n"
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
		if m.cancelRequested {
			return styleDim.Render("cancelling… ctrl+c again to quit")
		}
		return styleDim.Render("streaming… ctrl+c cancel · twice to quit")
	}
	if m.slashList != nil {
		return styleDim.Render("↑↓ select · enter run (or apply) · tab complete · esc clear")
	}
	if sidebarActive(m.width) {
		return styleDim.Render("滚轮切换区 · d 子任务 · /status · ctrl+c quit")
	}
	return styleDim.Render("enter send · double-enter newline · ctrl+v paste · ctrl+c quit · /exit")
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
	for _, c := range []struct {
		n string
		a []string
	}{
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
