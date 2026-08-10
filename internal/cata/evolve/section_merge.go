package evolve

import (
	"fmt"
	"strings"
)

// mergeMarkdownSection 若已有同名 ## 标题则替换该节正文，否则在文末追加。
func mergeMarkdownSection(body []byte, sectionName, content string) []byte {
	sectionName = strings.TrimSpace(sectionName)
	content = strings.TrimSpace(content)
	if sectionName == "" {
		sectionName = "Notes"
	}
	newBlock := fmt.Sprintf("## %s\n\n%s", sectionName, content)

	s := strings.ReplaceAll(string(body), "\r\n", "\n")
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return []byte(newBlock + "\n")
	}

	start, end := findMarkdownSectionRange(s, sectionName)
	if start < 0 {
		return []byte(s + "\n\n" + newBlock + "\n")
	}

	lines := strings.Split(s, "\n")
	var b strings.Builder
	for i := 0; i < start; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(lines[i])
	}
	if start > 0 {
		b.WriteByte('\n')
	}
	b.WriteString(newBlock)
	for i := end; i < len(lines); i++ {
		b.WriteByte('\n')
		b.WriteString(lines[i])
	}
	out := strings.TrimRight(b.String(), "\n")
	if out != "" {
		out += "\n"
	}
	return []byte(out)
}

// deleteMarkdownSection 删除同名 ## 节（含标题与正文）；不存在则原样返回。
func deleteMarkdownSection(body []byte, sectionName string) []byte {
	sectionName = strings.TrimSpace(sectionName)
	if sectionName == "" {
		return body
	}

	s := strings.ReplaceAll(string(body), "\r\n", "\n")
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return body
	}

	start, end := findMarkdownSectionRange(s, sectionName)
	if start < 0 {
		return body
	}

	lines := strings.Split(s, "\n")
	var b strings.Builder
	for i := 0; i < start; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(lines[i])
	}
	for i := end; i < len(lines); i++ {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(lines[i])
	}
	out := strings.TrimRight(b.String(), "\n")
	if out != "" {
		out += "\n"
	}
	return []byte(out)
}

func findMarkdownSectionRange(s, sectionName string) (start, end int) {
	header := "## " + sectionName
	lines := strings.Split(s, "\n")
	start = -1
	end = len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if sectionHeadingMatches(trimmed, header) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 && strings.HasPrefix(trimmed, "## ") {
			end = i
			break
		}
	}
	return start, end
}

func sectionHeadingMatches(trimmedLine, header string) bool {
	if trimmedLine == header {
		return true
	}
	// ## Preferences (updated) 等仍视为同一节
	if strings.HasPrefix(trimmedLine, header+" ") || strings.HasPrefix(trimmedLine, header+"\t") {
		return true
	}
	return false
}
