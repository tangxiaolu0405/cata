package brain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TerminalBundleSystemPrefix 兼容旧 llm.log 检测（已拆为引导/项目内容两段）。
const TerminalBundleSystemPrefix = "【Cata 脑子节选"

// TerminalGuidanceSystemPrefix ~/.cata/global 引导型提示词节选前缀。
const TerminalGuidanceSystemPrefix = "【Cata 引导 · ~/.cata/global】"

// TerminalProjectContentSystemPrefix 项目 .cata 主要内容提示词节选前缀。
const TerminalProjectContentSystemPrefix = "【Cata 项目内容"

// CompactExcessiveNewlines 将连续 3 个及以上的换行压成至多 2 个。
func CompactExcessiveNewlines(s string) string {
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var b strings.Builder
	b.Grow(len(s))
	consecNL := 0
	for _, r := range s {
		if r == '\n' {
			consecNL++
			if consecNL <= 2 {
				b.WriteByte('\n')
			}
		} else {
			consecNL = 0
			b.WriteRune(r)
		}
	}
	return b.String()
}

// TerminalBrainSystemExtension 组装出站 system 节选（使用当前 ActivePromptProfile）。
func TerminalBrainSystemExtension(maxPerFile, maxTotal int) string {
	return TerminalBrainSystemExtensionFor(ActivePromptProfile(), maxPerFile, maxTotal)
}

// TerminalBrainSystemExtensionFor 按指定 profile 组装节选（供 worker minimal 注入，避免全局 profile 竞态）。
func TerminalBrainSystemExtensionFor(p PromptProfile, maxPerFile, maxTotal int) string {
	if maxPerFile <= 0 {
		maxPerFile = 6500
	}
	if maxTotal <= 0 {
		maxTotal = 20000
	}

	var b strings.Builder
	used := 0

	appendBlock := func(s string) bool {
		s = strings.TrimSpace(s)
		if s == "" {
			return true
		}
		if used+len(s) > maxTotal {
			b.WriteString("\n\n## (省略)\n后续节选因 maxTotal 上限未载入。")
			return false
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(s)
		used += len(s)
		return true
	}

	paths := TerminalPathsSystemBlockFor(p)
	if !appendBlock(paths) {
		return b.String()
	}

	switch ProfileRank(p) {
	case 0:
		return b.String()
	case 1:
		if w := Active(); w != nil {
			caps := LoadActiveCapabilitiesCached()
			if skills := SkillsIndexBlockCached(caps.Skills); !appendBlock(skills) {
				return b.String()
			}
		}
		if idx := MemoryIndexPromptBlock(maxIndexPromptBytes); strings.TrimSpace(idx) != "" {
			if !appendBlock(idx) {
				return b.String()
			}
		}
		return b.String()
	}

	if w := Active(); w != nil {
		caps := LoadActiveCapabilitiesCached()
		if skills := SkillsIndexBlockCached(caps.Skills); !appendBlock(skills) {
			return b.String()
		}
	}
	if idx := MemoryIndexPromptBlock(maxIndexPromptBytes); strings.TrimSpace(idx) != "" {
		if !appendBlock(idx) {
			return b.String()
		}
	}

	if guidance := terminalGuidanceExcerpt(maxPerFile, maxTotal-used); !appendBlock(guidance) {
		return b.String()
	}
	if content := terminalProjectContentExcerpt(maxPerFile, maxTotal-used); content != "" {
		if !appendBlock(content) {
			return b.String()
		}
	}
	return b.String()
}

// terminalGuidanceExcerpt ~/.cata/global 引导型：constraints + behavior（不由 evolve 写入）。
func terminalGuidanceExcerpt(maxPerFile, budget int) string {
	sections := []struct {
		title string
		path  string
	}{}
	if p := GlobalConstraintsPath(); fileExists(p) {
		sections = append(sections, struct{ title, path string }{"global/constraints", p})
	}
	if p := GlobalBehaviorPath(); fileExists(p) {
		sections = append(sections, struct{ title, path string }{"global/behavior", p})
	}
	if len(sections) == 0 {
		return ""
	}
	var blocks []string
	used := 0
	for _, sec := range sections {
		block := readSection(sec.title, sec.path, maxPerFile)
		if budget > 0 && used+len(block) > budget {
			blocks = append(blocks, "## (省略)\n后续引导节选因体积上限未载入。")
			break
		}
		blocks = append(blocks, block)
		used += len(block)
	}
	var b strings.Builder
	b.WriteString(TerminalGuidanceSystemPrefix)
	b.WriteString("\n\n")
	b.WriteString(strings.Join(blocks, "\n\n"))
	return b.String()
}

// terminalProjectContentExcerpt focus_path/.cata 主要内容：mode persona/behavior/constraints + persona.local。
func terminalProjectContentExcerpt(maxPerFile, budget int) string {
	w := Active()
	if w == nil {
		return legacyProjectContentFallback(maxPerFile, budget)
	}

	sections := []struct {
		title string
		path  string
	}{}
	if p := w.PersonaPath(); fileExists(p) {
		sections = append(sections, struct{ title, path string }{
			fmt.Sprintf("mode/%s/persona", w.modeID()), p,
		})
	}
	modeDir := w.ModeDir(w.modeID())
	if p := filepath.Join(modeDir, FileBehavior); fileExists(p) && !FileNeedsEvolveFill(p) {
		sections = append(sections, struct{ title, path string }{
			fmt.Sprintf("mode/%s/behavior", w.modeID()), p,
		})
	}
	if p := filepath.Join(modeDir, FileConstraints); fileExists(p) && !FileNeedsEvolveFill(p) {
		sections = append(sections, struct{ title, path string }{
			fmt.Sprintf("mode/%s/constraints", w.modeID()), p,
		})
	}
	if p := w.PersonaLocalPath(); fileExists(p) {
		sections = append(sections, struct{ title, path string }{"persona.local (focus)", p})
	}
	if len(sections) == 0 {
		return ""
	}

	var blocks []string
	used := 0
	for _, sec := range sections {
		block := readSection(sec.title, sec.path, maxPerFile)
		if budget > 0 && used+len(block) > budget {
			blocks = append(blocks, "## (省略)\n后续项目内容节选因体积上限未载入。")
			break
		}
		blocks = append(blocks, block)
		used += len(block)
	}
	var b strings.Builder
	b.WriteString(TerminalProjectContentSystemPrefix)
	b.WriteString(" · ")
	b.WriteString(w.ProjectCataRoot())
	b.WriteString("】\n\n")
	b.WriteString(strings.Join(blocks, "\n\n"))
	return b.String()
}

func legacyProjectContentFallback(maxPerFile, budget int) string {
	sections := []struct {
		title string
		rel   string
	}{}
	for _, rel := range []struct{ title, rel string }{
		{"brain/constraints.md", RelPathConstraints},
		{"brain/behavior.md", RelPathBehavior},
		{"brain/hot.md", RelPathHot},
	} {
		p := Path(rel.rel)
		if fileExists(p) {
			sections = append(sections, struct{ title, rel string }{rel.title, rel.rel})
		}
	}
	if len(sections) == 0 {
		return ""
	}
	var blocks []string
	used := 0
	for _, sec := range sections {
		block := readSection(sec.title, Path(sec.rel), maxPerFile)
		if budget > 0 && used+len(block) > budget {
			break
		}
		blocks = append(blocks, block)
		used += len(block)
	}
	return TerminalProjectContentSystemPrefix + " · legacy】\n\n" + strings.Join(blocks, "\n\n")
}

func readSection(title, path string, maxPerFile int) string {
	b, err := os.ReadFile(path)
	var block string
	if err != nil {
		block = fmt.Sprintf("## %s\n(未能读取 %s: %v)", title, path, err)
	} else {
		body := CompactExcessiveNewlines(string(b))
		if len(body) > maxPerFile {
			body = body[:maxPerFile] + "\n…(truncated)"
		}
		block = "## " + title + "\n" + body
	}
	return block
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// BrainExcerptInjected 检测 messages 是否已含 cata 脑子节选（路径/引导/项目内容）。
func BrainExcerptInjected(content string) bool {
	c := strings.TrimSpace(content)
	return strings.HasPrefix(c, TerminalPathsSystemPrefix) ||
		strings.HasPrefix(c, TerminalBundleSystemPrefix) ||
		strings.HasPrefix(c, TerminalGuidanceSystemPrefix) ||
		strings.HasPrefix(c, TerminalProjectContentSystemPrefix)
}
