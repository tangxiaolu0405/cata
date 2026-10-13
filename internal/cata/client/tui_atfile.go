package client

import (
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// atQuery 描述当前输入中正在编辑的 `@token`：从最后一个 `@` 到行尾（或光标处）。
// ok=false 表示当前行没有处于「@补全」状态。
//
// 触发规则（与 splitAttachmentTokens 的识别保持一致，避免「补全出来却不被识别」）：
//   - `@` 必须位于行首或前一个字符是空白；
//   - `@` 之后到行尾不能含空白（含空白说明 token 已结束，不再补全）。
type atQuery struct {
	token string // `@` 之后的已输入片段（可为空）
	ok    bool
}

// currentAtQuery 取输入框当前行（最后一行）的 @token。
// 多行输入时只对光标所在行生效；这里简化为「最后一行」，与 slash 菜单只看首行对称。
func currentAtQuery(val string) atQuery {
	line := val
	if i := strings.LastIndex(val, "\n"); i >= 0 {
		line = val[i+1:]
	}
	at := strings.LastIndex(line, "@")
	if at < 0 {
		return atQuery{}
	}
	// `@` 前必须是行首或空白。
	if at > 0 {
		prev := line[at-1]
		if prev != ' ' && prev != '\t' {
			return atQuery{}
		}
	}
	token := line[at+1:]
	if strings.ContainsAny(token, " \t") {
		return atQuery{}
	}
	return atQuery{token: token, ok: true}
}

// atMatches 返回当前 @token 的候选文件（受 limit 限制）。
func (m *model) atMatches() []string {
	q := currentAtQuery(m.input.Value())
	if !q.ok || m.fileIdx == nil {
		return nil
	}
	return matchFiles(m.fileIdx.Files(), q.token, atMenuMaxItems)
}

// syncAtList 用 bubbles/list 渲染 @文件 候选菜单；无候选时关闭。
func (m *model) syncAtList() tea.Cmd {
	if m.streaming || m.overlay != nil {
		m.atList = nil
		return nil
	}
	// / 命令菜单打开时不叠加 @ 菜单（两者同占输入区，互斥）。
	if len(m.slashMatches()) > 0 {
		m.atList = nil
		return nil
	}
	matches := m.atMatches()
	if len(matches) == 0 {
		m.atList = nil
		return nil
	}
	items := make([]list.Item, 0, len(matches))
	for _, p := range matches {
		items = append(items, pickItem{id: p, title: "@" + p, desc: fileHint(p)})
	}
	w := m.mainInputWidth()
	if w < 20 {
		w = 20
	}
	h := min(atMenuMaxRows, len(matches)+1)
	if m.atList == nil {
		l := list.New(items, newSlashCmdDelegate(), w, h)
		l.SetShowTitle(false)
		l.SetShowFilter(false)
		l.SetFilteringEnabled(false)
		l.SetShowHelp(false)
		l.SetShowStatusBar(false)
		m.atList = &l
		return nil
	}
	return m.atList.SetItems(items)
}

// updateAtKeys 在 @ 菜单打开时处理 ↑↓/Tab/Esc/Enter。
// 返回 handled=true 表示按键已被消费，不再走普通编辑逻辑。
func (m *model) updateAtKeys(msg tea.KeyMsg) (bool, tea.Cmd) {
	if m.atList == nil {
		return false, nil
	}
	switch msg.String() {
	case "tab", "enter":
		// 已输入完整路径（token 精确命中某文件）：Enter 交给发送逻辑，避免再补一次。
		if msg.String() == "enter" && m.atTokenComplete() {
			return false, nil
		}
		// Tab 补全；Enter 在菜单打开时也补全（而非发送），避免误发半截路径。
		if it, ok := m.atList.SelectedItem().(pickItem); ok {
			m.applyAtCompletion(it.id)
			return true, m.syncAtList()
		}
		return true, nil
	case "esc":
		m.atList = nil
		return true, nil
	case "up", "down":
		var cmd tea.Cmd
		*m.atList, cmd = m.atList.Update(msg)
		return true, cmd
	}
	return false, nil
}

// applyAtCompletion 把当前 @token 替换为完整路径（`@<path>`），并补一个尾随空格便于继续输入。
func (m *model) applyAtCompletion(path string) {
	val := m.input.Value()
	lineStart := 0
	if i := strings.LastIndex(val, "\n"); i >= 0 {
		lineStart = i + 1
	}
	line := val[lineStart:]
	at := strings.LastIndex(line, "@")
	if at < 0 {
		return
	}
	prefix := val[:lineStart+at] // 含 `@`
	m.input.SetValue(prefix + "@" + path + " ")
	m.syncInputSize()
}

// atMenuLines 菜单占用的总行数（分隔线 1 行 + 列表实测高度）。
func (m *model) atMenuLines() int {
	if m.atList == nil {
		return 0
	}
	h := lipgloss.Height(m.atList.View())
	if h < 1 {
		return 1
	}
	return 1 + h
}

// atTokenComplete 判断当前 @token 是否已精确等于索引中的某个文件路径。
func (m *model) atTokenComplete() bool {
	q := currentAtQuery(m.input.Value())
	if !q.ok || q.token == "" || m.fileIdx == nil {
		return false
	}
	// 直接命中（大小写敏感）即视为完整；避免用 matchFiles 的模糊排序误判。
	for _, f := range m.fileIdx.Files() {
		if f == q.token {
			return true
		}
	}
	return false
}

// fileHint 给候选行右侧的提示：目录名（区分同名文件）。
func fileHint(path string) string {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return ""
	}
	return dir + "/"
}

// atMenuMaxItems / atMenuMaxRows 控制候选数量与菜单高度。
const (
	atMenuMaxItems = 200
	atMenuMaxRows  = 8
)
