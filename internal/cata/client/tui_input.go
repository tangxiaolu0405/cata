package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"cata/internal/cata/config"
)

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
	// /attach <path> 是带参数命令，须在 matchSlash 精确匹配前处理。
	if strings.HasPrefix(trimmed, "/attach") {
		return m.handleAttachCmd(trimmed), nil
	}
	if name, ok := matchSlash(trimmed); ok && !strings.Contains(trimmed, "\n") {
		switch name {
		case "exit", "quit", "q":
			return m, tea.Quit
		case "clear", "reset":
			r, err := m.sess.call(req{Command: "chat_reset"})
			if err != nil {
				m.appendLog(styleErr.Render("! "+err.Error())+"\n", true)
				return m, nil
			}
			if !r.Success {
				m.appendLog(styleErr.Render("! "+r.Message)+"\n", true)
				return m, nil
			}
			m.stats.turns, m.stats.round, m.stats.tools = 0, 0, 0
			m.stats.sessionTok = 0
			m.stats.state = "ready"
			m.thinkingActive = false
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
	// 附件：同行内 `@<path>` 提取 + /attach 队列合并，随 chat 发送。
	sendText, inlinePaths := splitAttachmentTokens(line)
	attachments := make([]attachReq, 0, len(inlinePaths)+len(m.attachQueue))
	for _, p := range inlinePaths {
		attachments = append(attachments, attachReq{Path: p})
	}
	for _, p := range m.attachQueue {
		attachments = append(attachments, attachReq{Path: p})
	}
	hadQueue := len(m.attachQueue) > 0
	queueTip := ""
	if hadQueue {
		queueTip = " (" + queueSummary(m.attachQueue) + ")"
	}
	m.attachQueue = nil // 发送即提交；失败可 /attach retry 或重输
	m.appendLog(styleUser.Render("you: ")+line+queueTip+"\n\n", true)
	m.streaming = true
	m.cancelRequested = false
	m.input.Blur()

	outCwd, _ := os.Getwd()
	if m.cwd != "" {
		outCwd = m.cwd
	}
	rt := m.runtime
	if err := m.sess.write(req{Command: "chat", Text: sendText, Attachments: attachments, Stream: true, Cwd: outCwd, Runtime: &rt, ShowThinking: m.showThinking}); err != nil {
		m.streaming = false
		m.cancelRequested = false
		m.input.Focus()
		m.appendLog(styleErr.Render("! "+err.Error())+"\n", true)
		return m, m.input.Focus()
	}
	return m, waitStream(m.sess)
}

// handleAttachCmd 处理 /attach 命令：加附件、清空、查看队列。
func (m *model) handleAttachCmd(cmd string) tea.Model {
	// 兼容 /attach clear / /attach list；空参数列出当前队列。
	fields := strings.Fields(strings.TrimSpace(cmd))
	arg := ""
	if len(fields) >= 2 {
		arg = strings.TrimPrefix(fields[1], "@")
	}
	switch arg {
	case "clear":
		m.attachQueue = nil
		m.appendLog("— attachment queue cleared\n", true)
	case "list", "":
		if len(m.attachQueue) == 0 {
			m.appendLog("— no pending attachments (use /attach <path> 或行内 @path)\n", true)
		} else {
			m.appendLog("— pending: "+queueSummary(m.attachQueue)+"\n", true)
		}
	default:
		m.attachQueue = append(m.attachQueue, arg)
		if len(m.attachQueue) > 12 {
			m.attachQueue = m.attachQueue[len(m.attachQueue)-12:]
		}
		m.appendLog("— queued ["+fmt.Sprintf("%d", len(m.attachQueue))+"] "+arg+" (发送时一并提交；/attach clear 清空)\n", true)
	}
	m.syncInputSize()
	return m
}

// queueSummary 渲染附件队列为 "[1] a.png, [2] b.png" 形式。
func queueSummary(paths []string) string {
	parts := make([]string, 0, len(paths))
	for i, p := range paths {
		parts = append(parts, fmt.Sprintf("[%d] %s", i+1, filepath.Base(p)))
	}
	return strings.Join(parts, ", ")
}

// splitAttachmentTokens 从输入行中提取 @<path> 附件 token（空格分隔），
// 从文本中剥离并返回路径列表。仅当 token 形如 @xxx 且去掉 @ 后非空时识别。
func splitAttachmentTokens(line string) (text string, paths []string) {
	fields := strings.Fields(line)
	var keep []string
	for _, f := range fields {
		if strings.HasPrefix(f, "@") && len(f) > 1 {
			paths = append(paths, strings.TrimPrefix(f, "@"))
			continue
		}
		keep = append(keep, f)
	}
	return strings.Join(keep, " "), paths
}
