package brain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// 会话历史「工具结果摘要化 + 溢出到文件」。
//
// 背景：多轮工具循环里，history 中的 tool 结果与老 assistant 正文会全文注入每轮请求，
// 长会话下迅速吃满 context。做法：超过阈值的轮次只保留一行摘要，原文落到产出区
// `.cata/session-spill/<sid>/` 下的文件，摘要里带相对产出区路径，模型需要细节时
// 用 `read_file` 展开。会话结束（chat_reset / 连接关闭）删除该会话的溢出目录。

const (
	// DirSessionSpill 会话溢出目录（相对产出区，位于项目 .cata 下）。
	DirSessionSpill = "session-spill"
	// SessionSpillRelPrefix 溢出路径在摘要里展示的前缀（相对产出区）。
	SessionSpillRelPrefix = ProjectCataDir + "/" + DirSessionSpill
)

// SessionSpillDir 返回某会话的溢出目录绝对路径（产出区下）。
func SessionSpillDir(outputCwd, sessionID string) string {
	sid := sanitizeSessionID(sessionID)
	if sid == "" {
		sid = "default"
	}
	return filepath.Join(outputCwd, ProjectCataDir, DirSessionSpill, sid)
}

// SessionSpillRelPath 返回溢出文件相对产出区的路径（用于写进摘要，供 read_file 使用）。
func SessionSpillRelPath(sessionID, fileName string) string {
	sid := sanitizeSessionID(sessionID)
	if sid == "" {
		sid = "default"
	}
	return SessionSpillRelPrefix + "/" + sid + "/" + fileName
}

// WriteSessionSpill 把一条消息正文写入会话溢出文件，返回相对产出区路径。
// 文件名由调用方给出（须已 sanitize，如 "003-tool-run_command.txt"）。
func WriteSessionSpill(outputCwd, sessionID, fileName, content string) (string, error) {
	if strings.TrimSpace(outputCwd) == "" {
		return "", fmt.Errorf("session spill: output cwd required")
	}
	dir := SessionSpillDir(outputCwd, sessionID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	abs := filepath.Join(dir, fileName)
	if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
		return "", err
	}
	return SessionSpillRelPath(sessionID, fileName), nil
}

// ClearSessionSpill 删除某会话的溢出目录（会话结束清理）。目录不存在视为成功。
func ClearSessionSpill(outputCwd, sessionID string) error {
	if strings.TrimSpace(outputCwd) == "" {
		return nil
	}
	dir := SessionSpillDir(outputCwd, sessionID)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// sanitizeSessionID 把会话 id 归一为安全目录名（仅保留字母数字与 -_）。
func sanitizeSessionID(sid string) string {
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range sid {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SpillFileName 生成溢出文件名：<序号>-<工具名>.txt（工具名归一，空则 message）。
func SpillFileName(seq int, toolName string) string {
	name := sanitizeSessionID(toolName)
	if name == "" {
		name = "message"
	}
	return fmt.Sprintf("%03d-%s.txt", seq, name)
}

// SpillSummary 生成一行摘要：说明原文已溢出、给出可 read_file 的相对路径、附首行预览。
// 这是注入 history 的替身正文，务必短（目标 ≤ 200 字节预览 + 固定前缀）。
func SpillSummary(relPath, original string, origBytes int) string {
	preview := firstMeaningfulLine(original)
	preview = truncateRunesLocal(preview, 160)
	if preview == "" {
		preview = "(无正文)"
	}
	return fmt.Sprintf("[已摘要] 原文 %d 字节已存 `%s`；需要细节用 read_file 展开。首行：%s",
		origBytes, relPath, preview)
}

// firstMeaningfulLine 取正文第一行有内容的行（跳过空行）。
func firstMeaningfulLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimSpace(ln)
		if t != "" {
			return t
		}
	}
	return ""
}

// truncateRunes 按码点截断（本地副本，避免依赖 llm 包）。
func truncateRunesLocal(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}
