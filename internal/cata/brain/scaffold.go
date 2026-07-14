package brain

import (
	"os"
	"strings"
	"unicode/utf8"
)

const minMeaningfulDocRunes = 48

// FileNeedsEvolveFill 文件不存在或仍为 scaffold/几乎无正文（应由演进 LLM 填充）。
func FileNeedsEvolveFill(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	return markdownMeaningfulRunes(string(data)) < minMeaningfulDocRunes
}

// markdownMeaningfulRunes 统计去掉标题、引用、空行后的正文码点数。
func markdownMeaningfulRunes(s string) int {
	s = CompactExcessiveNewlines(s)
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, ">") {
			continue
		}
		if strings.HasPrefix(line, "---") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return utf8.RuneCountInString(strings.TrimSpace(b.String()))
}
