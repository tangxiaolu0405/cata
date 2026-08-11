package client

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestStyledLogLineNoRightShift 回归：超时后输入「继续」，TUI 输出不应右移。
//
// 根因：lipgloss.Style.Render 会把多行文本的每一行补齐到最宽行；若样式内文本以
// "\n" 结尾，末尾空行会被补成空格，这些空格与后续追加的内容（如 "you: 继续"）合并，
// 导致整行右移。styledLogLine 把行尾换行移到样式外，杜绝该泄漏。
func TestStyledLogLineNoRightShift(t *testing.T) {
	m := newModel(&session{}, "/tmp/proj")
	m.width = 120
	m.height = 30
	m.displayMode = ""
	// 模拟真实场景：error（超时提示）→ done 失败 → 用户输入「继续」→ 助手继续输出。
	m.appendLog(styleErr.Render("! "+timeoutErrMsg)+"\n", true)
	m.appendLog(styledLogLine(styleErr, "\n! chat failed"), true)
	m.appendLog(styleUser.Render("you: ")+"继续"+"\n\n", true)
	m.appendLog("好的，继续执行。", false)
	m.appendLog("\n\n下一步我会用 list_files 查看目录。", false)
	m.appendLog("\n", true)

	lines := strings.Split(m.View(), "\n")
	var userLine string
	for _, ln := range lines {
		// 主区内容在圆角边框内；去掉左边框与右侧栏后检查 "you:" 行。
		clean := strings.TrimPrefix(ln, "│")
		if strings.Contains(clean, "you: 继续") {
			userLine = clean
			break
		}
	}
	if userLine == "" {
		t.Fatalf("未找到 'you: 继续' 行：\n%v", strings.Join(lines, "\n"))
	}
	// 行首必须是 "you: "，不能被空格右移。
	if !strings.HasPrefix(userLine, "you: ") {
		t.Fatalf("'you: 继续' 行被右移：%q\n完整输出：\n%v", userLine, strings.Join(lines, "\n"))
	}
}

// TestStyledLogLineTrimsTrailingNewline 确保 helper 把行尾换行移出样式。
func TestStyledLogLineTrimsTrailingNewline(t *testing.T) {
	st := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	got := styledLogLine(st, "\n! chat failed\n")
	if !strings.HasSuffix(got, "\n") {
		t.Fatalf("expected trailing newline, got %q", got)
	}
	if strings.Contains(got, "\n\n") {
		t.Fatalf("styled string should not end with newline inside style, got %q", got)
	}
}
