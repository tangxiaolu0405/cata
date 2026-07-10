package brain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cata/internal/clock"
)

const (
	maxTurnUserRunes       = 600
	maxTurnAssistantRunes  = 2000
	maxShortTermFileBytes  = 96 * 1024
	shortTermFileHeader    = "# Current session (short-term)\n\n> Appended by cata after each chat turn. Consolidated into persona by autonomous evolution.\n\n"
	// DefaultKeepRecentAfterConsolidate 演进归档后保留在 short-term 的尾部字节（最近几轮对话）。
	DefaultKeepRecentAfterConsolidate = 2048
)

// AppendChatTurn 在对话成功结束后写入当前 workspace 的 short-term。
func AppendChatTurn(userText, assistantText string) error {
	w, err := MustActive()
	if err != nil {
		return err
	}
	return AppendChatTurnFor(w, userText, assistantText)
}

// AppendChatTurnFor 向指定 workspace 追加回合。
func AppendChatTurnFor(w *Workspace, userText, assistantText string) error {
	userText = truncateRunes(strings.TrimSpace(userText), maxTurnUserRunes)
	assistantText = truncateRunes(strings.TrimSpace(assistantText), maxTurnAssistantRunes)
	if userText == "" && assistantText == "" {
		return nil
	}
	block := formatTurnBlock(userText, assistantText)
	return appendToShortTerm(w.ShortTermPath(), block)
}

// AppendSessionBoundary 在 chat_reset 时写入会话边界。
func AppendSessionBoundary() error {
	w, err := MustActive()
	if err != nil {
		return err
	}
	path := w.ShortTermPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	ts := clock.RFC3339()
	block := fmt.Sprintf("\n\n---\n\n**Session ended** `%s` (socket history cleared)\n\n", ts)
	return appendToShortTerm(path, block)
}

func formatTurnBlock(user, assistant string) string {
	ts := clock.RFC3339()
	var b strings.Builder
	b.WriteString("\n\n## ")
	b.WriteString(ts)
	b.WriteString("\n\n")
	if user != "" {
		b.WriteString("**User:** ")
		b.WriteString(user)
		b.WriteString("\n\n")
	}
	if assistant != "" {
		b.WriteString("**Assistant:** ")
		b.WriteString(assistant)
		b.WriteString("\n\n")
	}
	return b.String()
}

func appendToShortTerm(path, block string) error {
	var existing []byte
	if data, err := os.ReadFile(path); err == nil {
		existing = data
	} else if !os.IsNotExist(err) {
		return err
	}

	body := existing
	if len(body) == 0 {
		body = []byte(shortTermFileHeader)
	} else if !strings.HasPrefix(string(body), "# ") {
		body = append([]byte(shortTermFileHeader), body...)
	}

	combined := append(body, []byte(block)...)
	if len(combined) > maxShortTermFileBytes {
		combined = trimShortTermToMax(combined, maxShortTermFileBytes)
	}
	return os.WriteFile(path, combined, 0644)
}

func trimShortTermToMax(data []byte, max int) []byte {
	if len(data) <= max {
		return data
	}
	marker := []byte("\n\n…(older turns trimmed)\n\n")
	keep := max - len(marker)
	if keep < len(shortTermFileHeader)+256 {
		keep = len(shortTermFileHeader) + 256
	}
	if keep > len(data) {
		keep = len(data)
	}
	out := make([]byte, 0, max)
	out = append(out, data[:len(shortTermFileHeader)]...)
	out = append(out, marker...)
	out = append(out, data[len(data)-keep+len(shortTermFileHeader):]...)
	if len(out) > max {
		out = out[len(out)-max:]
	}
	return out
}

func truncateRunes(s string, max int) string {
	if max <= 0 || s == "" {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// EnsureShortTermFileFor 确保 workspace short-term 存在。
func EnsureShortTermFileFor(w *Workspace) error {
	path := w.ShortTermPath()
	_, err := os.Stat(path)
	if err == nil {
		return nil
	}
	if os.IsNotExist(err) {
		return os.WriteFile(path, []byte(shortTermFileHeader), 0644)
	}
	return err
}

// FinalizeShortTermAfterConsolidate 将当前 short-term 归档到 memory/archive/ 并重置文件，避免演进重复喂同一段原文。
// keepRecentBytes 为 0 时使用 DefaultKeepRecentAfterConsolidate。
func FinalizeShortTermAfterConsolidate(keepRecentBytes int) (archivedRel string, err error) {
	w, err := MustActive()
	if err != nil {
		return "", err
	}
	if keepRecentBytes <= 0 {
		keepRecentBytes = DefaultKeepRecentAfterConsolidate
	}
	path := w.ShortTermPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if len(data) < len(shortTermFileHeader)+256 {
		return "", nil
	}

	ts := clock.RFC3339()
	archiveName := fmt.Sprintf("consolidated-%s.md", clock.Format("2006-01-02-150405"))
	archivedRel = filepath.Join(RelMemoryArchive, archiveName)
	archiveAbs := w.Path(archivedRel)
	if err := os.MkdirAll(filepath.Dir(archiveAbs), 0755); err != nil {
		return "", err
	}
	archiveDoc := fmt.Sprintf("# Short-term archive\n\n> Archived at %s after evolution consolidate.\n\n%s",
		ts, string(data))
	if err := os.WriteFile(archiveAbs, []byte(archiveDoc), 0644); err != nil {
		return "", err
	}

	recent := tailFromTurnBoundary(data, keepRecentBytes)
	var b strings.Builder
	b.WriteString(shortTermFileHeader)
	b.WriteString("> Last consolidated `")
	b.WriteString(ts)
	b.WriteString("`. Older turns moved to `")
	b.WriteString(filepath.ToSlash(archivedRel))
	b.WriteString("`. Recent turns below.\n\n")
	if len(recent) > 0 {
		b.Write(recent)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return archivedRel, err
	}
	return archivedRel, nil
}

// tailFromTurnBoundary 取文件尾部，尽量从 "## " 回合标题处切开。
func tailFromTurnBoundary(data []byte, maxBytes int) []byte {
	if maxBytes <= 0 || len(data) <= maxBytes {
		return data
	}
	tail := data[len(data)-maxBytes:]
	if idx := strings.Index(string(tail), "\n\n## "); idx > 0 {
		return tail[idx+2:]
	}
	return tail
}

// EnsureShortTermFile 活跃 workspace 或 legacy 路径。
func EnsureShortTermFile() error {
	if w := Active(); w != nil {
		return EnsureShortTermFileFor(w)
	}
	path := ShortTermCurrentPath()
	_, err := os.Stat(path)
	if err == nil {
		return nil
	}
	if os.IsNotExist(err) {
		return os.WriteFile(path, []byte(shortTermFileHeader), 0644)
	}
	return err
}
