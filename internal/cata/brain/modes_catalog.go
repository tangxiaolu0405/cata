package brain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	ModesCatalogPrefix     = "【Cata 专职 Modes】"
	maxModesCatalogBytes   = 1400
	LongMemoryHotPrefix    = "【Cata 长期记忆节选】"
	maxLongMemoryHotBytes  = 1800
	maxLearningHotBullets  = 12
	maxLearningHotRunes    = 900
)

// HasSpecialistModes 项目是否有除 _default 外的专职 mode。
func HasSpecialistModes(w *Workspace) bool {
	if w == nil {
		return false
	}
	modes, err := ListProjectModes(w)
	if err != nil {
		return false
	}
	for _, m := range modes {
		if NormalizeModeID(m.ID) != ModeDefaultID {
			return true
		}
	}
	return false
}

// ModesCatalogPromptBlock 注入主 Agent：专职 mode 目录 + 委派提示（不含全文 SOP）。
func ModesCatalogPromptBlock(w *Workspace) string {
	if w == nil {
		return ""
	}
	modes, err := ListProjectModes(w)
	if err != nil || len(modes) == 0 {
		return ""
	}
	var specs []ModeInfo
	for _, m := range modes {
		if NormalizeModeID(m.ID) == ModeDefaultID {
			continue
		}
		specs = append(specs, m)
	}
	if len(specs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(ModesCatalogPrefix)
	b.WriteString("\n\n")
	b.WriteString("主对话人格是 `_default`。下列专职 **不会**自动注入；匹配时须 `delegate_task`（`mode_id`+`case_id`）或 `delegate_mode`，再用结果整合回复。勿把专职 SOP 重做一遍。\n\n")
	used := b.Len()
	for _, m := range specs {
		line := m.OneLiner
		if line == "" {
			line = "(see modes/" + m.ID + "/persona.md)"
		}
		draft := ""
		if _, err := os.Stat(filepath.Join(w.ModeDir(m.ID), ".draft")); err == nil {
			draft = " [draft]"
		}
		entry := fmt.Sprintf("- **%s**%s — %s\n  → `delegate_task` mode_id=%s case_id=<id> task=…\n", m.ID, draft, line, m.ID)
		if used+len(entry) > maxModesCatalogBytes {
			b.WriteString("- …(更多 mode 未列入)\n")
			break
		}
		b.WriteString(entry)
		used += len(entry)
	}
	return strings.TrimSpace(b.String())
}

// LongMemoryHotPromptBlock 注入近期长期记忆正文节选（不只 index 一行摘要）。
func LongMemoryHotPromptBlock(w *Workspace, maxBytes int) string {
	if w == nil {
		return ""
	}
	if maxBytes <= 0 {
		maxBytes = maxLongMemoryHotBytes
	}
	var parts []string
	if hot := recentLearningsExcerpt(w.Path(RelMemoryLongLearnings), maxLearningHotBullets, maxLearningHotRunes); hot != "" {
		parts = append(parts, "### memory/long/learnings.md（近条）\n"+hot)
	}
	if notes := headFileExcerpt(w.Path(RelMemoryLongSessionNotes), 600); notes != "" {
		parts = append(parts, "### memory/long/session-notes.md\n"+notes)
	}
	// 其它 long/*.md：只列文件名提示，避免撑爆
	if extras := listOtherLongMemoryHints(w, 6); extras != "" {
		parts = append(parts, "### 其它 long 文件（需时 `read_file brain/memory/long/…`）\n"+extras)
	}
	if len(parts) == 0 {
		return ""
	}
	body := strings.Join(parts, "\n\n")
	if len(body) > maxBytes {
		body = body[:maxBytes] + "\n…(truncated)"
	}
	return LongMemoryHotPrefix + "\n\n> 可复用事实若已进 persona/behavior 以项目内容为准；此处是审计/会话沉淀，相关时优先引用或 `read_file`。\n\n" + body
}

func recentLearningsExcerpt(path string, maxBullets, maxRunes int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	var bullets []string
	for i := len(lines) - 1; i >= 0; i-- {
		ln := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(ln, "- ") && !strings.HasPrefix(ln, "* ") {
			continue
		}
		bullets = append(bullets, ln)
		if len(bullets) >= maxBullets {
			break
		}
	}
	if len(bullets) == 0 {
		return ""
	}
	// reverse to chronological
	for i, j := 0, len(bullets)-1; i < j; i, j = i+1, j-1 {
		bullets[i], bullets[j] = bullets[j], bullets[i]
	}
	out := strings.Join(bullets, "\n")
	if utf8.RuneCountInString(out) > maxRunes {
		r := []rune(out)
		out = string(r[:maxRunes]) + "…"
	}
	return out
}

func headFileExcerpt(path string, maxBytes int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := CompactExcessiveNewlines(strings.TrimSpace(string(data)))
	if s == "" {
		return ""
	}
	// skip pure header-only
	body := s
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, ">") {
			continue
		}
		body = s
		break
	}
	if len(body) > maxBytes {
		body = body[:maxBytes] + "…"
	}
	return body
}

func listOtherLongMemoryHints(w *Workspace, limit int) string {
	dir := w.LongTermDir()
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var lines []string
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := e.Name()
		if name == "learnings.md" || name == "session-notes.md" {
			continue
		}
		lines = append(lines, "- `brain/memory/long/"+name+"`")
		if len(lines) >= limit {
			break
		}
	}
	return strings.Join(lines, "\n")
}

// EnsureDefaultSpecialistRoute 在 _default/behavior.md 登记专职委派路由（幂等）。
func EnsureDefaultSpecialistRoute(w *Workspace, modeID, oneLiner string) error {
	if w == nil {
		return fmt.Errorf("workspace required")
	}
	id := NormalizeModeID(modeID)
	if id == "" || id == ModeDefaultID {
		return fmt.Errorf("invalid specialist mode id")
	}
	if oneLiner == "" {
		oneLiner = firstPersonaLine(filepath.Join(w.ModeDir(id), FilePersona))
	}
	if oneLiner == "" {
		oneLiner = "specialist work"
	}
	if utf8.RuneCountInString(oneLiner) > 80 {
		oneLiner = string([]rune(oneLiner)[:80]) + "…"
	}
	path := filepath.Join(w.ModeDir(ModeDefaultID), FileBehavior)
	data, err := os.ReadFile(path)
	body := ""
	if err == nil {
		body = string(data)
	} else if !os.IsNotExist(err) {
		return err
	}
	routeLine := fmt.Sprintf("- `%s` — %s → `delegate_task` mode_id=%s case_id=<slug>", id, oneLiner, id)
	const section = "## Specialist modes"
	if strings.Contains(body, section) {
		// replace existing bullet for this mode or append under section
		lines := strings.Split(body, "\n")
		var out []string
		inSec := false
		replaced := false
		for i := 0; i < len(lines); i++ {
			ln := lines[i]
			trim := strings.TrimSpace(ln)
			if strings.HasPrefix(trim, "## ") {
				if inSec && !replaced {
					out = append(out, routeLine)
					replaced = true
				}
				inSec = trim == section
				out = append(out, ln)
				continue
			}
			if inSec && (strings.HasPrefix(trim, "- `"+id+"`") || strings.Contains(trim, "mode_id="+id)) {
				out = append(out, routeLine)
				replaced = true
				continue
			}
			out = append(out, ln)
		}
		if inSec && !replaced {
			out = append(out, routeLine)
		}
		body = strings.Join(out, "\n")
	} else {
		if body != "" && !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		body += "\n" + section + "\n\n" +
			"专职 SOP 在 `modes/<id>/`；匹配时委派，勿在 `_default` 重做。\n\n" +
			routeLine + "\n"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(CompactExcessiveNewlines(body)+"\n"), 0644)
}
