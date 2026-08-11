package brain

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	skillFileName            = "SKILL.md"
	maxBytesPerSkillSummary  = 300
	maxBytesSkillsIndexTotal = 2048
	SkillsIndexPrefix        = "【Cata Skills 索引】"
)

var (
	skillsIndexCacheMu    sync.RWMutex
	skillsIndexCacheKey   string
	skillsIndexCacheBlock string
)

// SkillsIndexBlockCached 返回 skill 索引块（带 mtime 缓存）。
func SkillsIndexBlockCached(skillNames []string) string {
	return SkillsIndexBlockCachedFor(Active(), skillNames)
}

// SkillsIndexBlockCachedFor 显式指定 workspace 的 skill 索引块（多 chat 并行勿依赖全局 Active）。
func SkillsIndexBlockCachedFor(w *Workspace, skillNames []string) string {
	key := buildSkillsIndexCacheKeyFor(w, skillNames)
	skillsIndexCacheMu.RLock()
	if skillsIndexCacheKey == key && skillsIndexCacheBlock != "" {
		b := skillsIndexCacheBlock
		skillsIndexCacheMu.RUnlock()
		return b
	}
	skillsIndexCacheMu.RUnlock()

	block := skillsIndexBlockFor(w, skillNames)
	skillsIndexCacheMu.Lock()
	skillsIndexCacheKey = key
	skillsIndexCacheBlock = block
	skillsIndexCacheMu.Unlock()
	return block
}

// SkillsPromptBlock 兼容旧名；现为索引模式。
func SkillsPromptBlock(skillNames []string) string {
	return SkillsIndexBlockCached(skillNames)
}

func skillsIndexBlock(skillNames []string) string {
	return skillsIndexBlockFor(Active(), skillNames)
}

func skillsIndexBlockFor(w *Workspace, skillNames []string) string {
	if len(skillNames) == 0 {
		return ""
	}
	var lines []string
	used := 0
	header := SkillsIndexPrefix + "\n\n全文：`read_skill`；执行：`run_skill`。\n"
	used += len(header)

	for _, name := range skillNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		path, summary, err := loadSkillIndexEntryFor(w, name)
		if err != nil {
			log.Printf("skills index: %q: %v", name, err)
			continue
		}
		line := fmt.Sprintf("- **%s** — `%s`\n  %s", name, path, summary)
		if used+len(line) > maxBytesSkillsIndexTotal {
			lines = append(lines, "- …(更多 skill 因体积上限未列入索引)")
			break
		}
		lines = append(lines, line)
		used += len(line)
	}
	if len(lines) == 0 {
		return ""
	}
	return header + "\n" + strings.Join(lines, "\n")
}

func loadSkillIndexEntry(name string) (path, summary string, err error) {
	return loadSkillIndexEntryFor(Active(), name)
}

func loadSkillIndexEntryFor(w *Workspace, name string) (path, summary string, err error) {
	body, from, err := loadSkillMarkdownFor(w, name)
	if err != nil {
		return "", "", err
	}
	return from, skillSummary(body), nil
}

func skillSummary(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return "（无摘要）"
	}
	lines := strings.Split(body, "\n")
	var parts []string
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			if len(parts) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(ln, "#") {
			continue
		}
		parts = append(parts, ln)
		if len(strings.Join(parts, " ")) >= maxBytesPerSkillSummary {
			break
		}
	}
	s := strings.Join(parts, " ")
	if s == "" {
		s = strings.TrimSpace(lines[0])
	}
	if len(s) > maxBytesPerSkillSummary {
		s = s[:maxBytesPerSkillSummary] + "…"
	}
	return s
}

func loadSkillMarkdown(name string) (body, from string, err error) {
	return loadSkillMarkdownFor(Active(), name)
}

func loadSkillMarkdownFor(w *Workspace, name string) (body, from string, err error) {
	for _, p := range skillSearchPathsFor(w, name) {
		data, e := os.ReadFile(p)
		if e == nil {
			return CompactExcessiveNewlines(strings.TrimSpace(string(data))), p, nil
		}
		if !os.IsNotExist(e) {
			err = e
		}
	}
	if err != nil {
		return "", "", err
	}
	return "", "", fmt.Errorf("SKILL.md not found for %q", name)
}

func skillSearchPaths(name string) []string {
	return skillSearchPathsFor(Active(), name)
}

func skillSearchPathsFor(w *Workspace, name string) []string {
	var paths []string
	if w != nil {
		paths = append(paths, w.SkillMarkdownPath(name))
	}
	paths = append(paths, GlobalSkillMarkdownPath(name))
	if h, e := os.UserHomeDir(); e == nil && h != "" {
		paths = append(paths, filepath.Join(h, ".cursor", "skills-cursor", name, skillFileName))
	}
	return paths
}

func buildSkillsIndexCacheKey(skillNames []string) string {
	return buildSkillsIndexCacheKeyFor(Active(), skillNames)
}

func buildSkillsIndexCacheKeyFor(w *Workspace, skillNames []string) string {
	var parts []string
	if w != nil {
		parts = append(parts, w.ID, w.modeID())
	}
	names := append([]string(nil), skillNames...)
	sort.Strings(names)
	parts = append(parts, strings.Join(names, ","))
	for _, name := range names {
		parts = append(parts, name+":"+strconv.FormatInt(skillFileModTimeNanoFor(w, name), 10))
	}
	return strings.Join(parts, "|")
}

func skillFileModTimeNano(name string) int64 {
	return skillFileModTimeNanoFor(Active(), name)
}

func skillFileModTimeNanoFor(w *Workspace, name string) int64 {
	for _, p := range skillSearchPathsFor(w, name) {
		if mt := fileModTimeNano(p); mt > 0 {
			return mt
		}
	}
	return 0
}
