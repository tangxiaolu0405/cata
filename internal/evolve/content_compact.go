package evolve

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"cata/internal/brain"
)

// projectContentCompactTriggerBytes 项目主要内容超过此值触发 compact 演进。
const projectContentCompactTriggerBytes = 3500

// IsProjectContentRel 是否为项目 .cata 主要内容路径（persona / behavior / constraints / persona.local）。
func IsProjectContentRel(rel string) bool {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	rel = strings.TrimPrefix(rel, "brain/")
	switch {
	case rel == brain.RelPersonaLocal:
		return true
	case strings.HasPrefix(rel, brain.DirModes+"/"):
		return strings.HasSuffix(rel, "/"+brain.FilePersona) ||
			strings.HasSuffix(rel, "/"+brain.FileBehavior) ||
			strings.HasSuffix(rel, "/"+brain.FileConstraints)
	default:
		return false
	}
}

// CompactMarkdown 去重 ## 节（保留后者）、去重段落（保留后者）、压空行。
func CompactMarkdown(body []byte) []byte {
	s := strings.ReplaceAll(string(body), "\r\n", "\n")
	s = strings.TrimSpace(s)
	if s == "" {
		return body
	}

	lines := strings.Split(s, "\n")
	var preamble []string
	type sec struct {
		title string
		body  string
	}
	var sections []sec
	var curTitle string
	var curLines []string
	flush := func() {
		if curTitle == "" {
			return
		}
		sections = append(sections, sec{
			title: curTitle,
			body:  dedupeParagraphs(strings.TrimSpace(strings.Join(curLines, "\n"))),
		})
		curTitle = ""
		curLines = nil
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			flush()
			curTitle = strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			continue
		}
		if curTitle == "" {
			preamble = append(preamble, line)
		} else {
			curLines = append(curLines, line)
		}
	}
	flush()

	// 同名节保留最后一次出现
	byKey := make(map[string]sec)
	var order []string
	for _, s := range sections {
		key := normalizeSectionKey(s.title)
		if _, ok := byKey[key]; !ok {
			order = append(order, key)
		}
		byKey[key] = s
	}

	var b strings.Builder
	pre := strings.TrimSpace(strings.Join(preamble, "\n"))
	if pre != "" {
		b.WriteString(pre)
	}
	for _, key := range order {
		s := byKey[key]
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("## ")
		b.WriteString(s.title)
		if s.body != "" {
			b.WriteString("\n\n")
			b.WriteString(s.body)
		}
	}
	out := strings.TrimRight(b.String(), "\n")
	if out != "" {
		out += "\n"
	}
	return []byte(out)
}

func normalizeSectionKey(title string) string {
	title = strings.TrimSpace(title)
	var b strings.Builder
	for _, r := range title {
		if unicode.IsSpace(r) {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func dedupeParagraphs(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	parts := strings.Split(text, "\n\n")
	seen := make(map[string]bool)
	var kept []string
	for i := len(parts) - 1; i >= 0; i-- {
		p := strings.TrimSpace(parts[i])
		if p == "" {
			continue
		}
		key := normalizeParagraphKey(p)
		if seen[key] {
			continue
		}
		seen[key] = true
		kept = append(kept, p)
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	return strings.Join(kept, "\n\n")
}

func normalizeParagraphKey(p string) string {
	p = strings.ToLower(p)
	var b strings.Builder
	lastSpace := false
	for _, r := range p {
		if unicode.IsSpace(r) {
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		lastSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// compactTouchedProjectContent 对本轮触及的项目主要内容做确定性去重。
func compactTouchedProjectContent(ws *brain.Workspace, touched []string) []string {
	if ws == nil {
		return nil
	}
	var compacted []string
	seen := make(map[string]bool)
	for _, rel := range touched {
		rel = filepath.ToSlash(rel)
		if !IsProjectContentRel(rel) || seen[rel] {
			continue
		}
		seen[rel] = true
		abs, err := brain.ResolveBrainDocAbs(ws, rel)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil || len(data) == 0 {
			continue
		}
		out := CompactMarkdown(data)
		if string(out) == string(data) {
			continue
		}
		if err := os.WriteFile(abs, out, 0644); err != nil {
			continue
		}
		compacted = append(compacted, rel)
	}
	return compacted
}

// appendProjectContentCompactTriggers 项目主要内容过大时要求演进简化。
func appendProjectContentCompactTriggers(s *Snapshot, ws *brain.Workspace) {
	if ws == nil {
		return
	}
	mode := brain.NormalizeModeID(ws.ActiveMode)
	checks := []struct {
		abs   string
		label string
	}{
		{ws.PersonaPath(), "persona"},
		{ws.PersonaLocalPath(), "persona.local"},
		{filepath.Join(ws.ModeDir(mode), brain.FileBehavior), "mode_behavior"},
		{filepath.Join(ws.ModeDir(mode), brain.FileConstraints), "mode_constraints"},
	}
	for _, c := range checks {
		info, err := os.Stat(c.abs)
		if err != nil {
			continue
		}
		if info.Size() >= projectContentCompactTriggerBytes {
			s.Triggers = append(s.Triggers,
				"compact:"+c.label+">="+strconv.Itoa(int(info.Size())))
		}
	}
}

func hasCompactTrigger(snap *Snapshot) bool {
	for _, t := range snap.Triggers {
		if strings.HasPrefix(t, "compact:") {
			return true
		}
	}
	return false
}
