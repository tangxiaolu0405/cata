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
	key := buildSkillsIndexCacheKey(skillNames)
	skillsIndexCacheMu.RLock()
	if skillsIndexCacheKey == key && skillsIndexCacheBlock != "" {
		b := skillsIndexCacheBlock
		skillsIndexCacheMu.RUnlock()
		return b
	}
	skillsIndexCacheMu.RUnlock()

	block := skillsIndexBlock(skillNames)
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
		path, summary, err := loadSkillIndexEntry(name)
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
	body, from, err := loadSkillMarkdown(name)
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
	for _, p := range skillSearchPaths(name) {
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
	var paths []string
	if w := Active(); w != nil {
		paths = append(paths, w.SkillMarkdownPath(name))
	}
	paths = append(paths, GlobalSkillMarkdownPath(name))
	if h, e := os.UserHomeDir(); e == nil && h != "" {
		paths = append(paths, filepath.Join(h, ".cursor", "skills-cursor", name, skillFileName))
	}
	return paths
}

func buildSkillsIndexCacheKey(skillNames []string) string {
	var parts []string
	if w := Active(); w != nil {
		parts = append(parts, w.ID, w.modeID())
	}
	names := append([]string(nil), skillNames...)
	sort.Strings(names)
	parts = append(parts, strings.Join(names, ","))
	for _, name := range names {
		parts = append(parts, name+":"+strconv.FormatInt(skillFileModTimeNano(name), 10))
	}
	return strings.Join(parts, "|")
}

func skillFileModTimeNano(name string) int64 {
	for _, p := range skillSearchPaths(name) {
		if mt := fileModTimeNano(p); mt > 0 {
			return mt
		}
	}
	return 0
}
