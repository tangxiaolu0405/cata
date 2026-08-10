package brain

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"cata/internal/cata/clock"
	"unicode/utf8"
)

const (
	memoryIndexVersion   = 1
	maxIndexEntries      = 200
	maxIndexSummaryRunes = 120
	maxIndexKeywords     = 16
	maxIndexPromptBytes  = 2800
)

// MemoryIndex 工作区记忆索引（memory/index.json）。
type MemoryIndex struct {
	Version   int          `json:"version"`
	UpdatedAt string       `json:"updated_at,omitempty"`
	Entries   []IndexEntry `json:"entries"`
}

// IndexEntry 单条索引（供 LLM 扫描后按需展开原文）。
type IndexEntry struct {
	ID              string   `json:"id"`
	Source          string   `json:"source"`
	Summary         string   `json:"summary"`
	Keywords        []string `json:"keywords,omitempty"`
	Category        string   `json:"category"`
	Priority        int      `json:"priority"`
	DisclosureLevel string   `json:"disclosure_level,omitempty"`
	UpdatedAt       string   `json:"updated_at,omitempty"`
}

// LoadMemoryIndex 读取当前 workspace 的 index.json（兼容旧版 `[]`）。
func LoadMemoryIndex() (*MemoryIndex, error) {
	w, err := MustActive()
	if err != nil {
		return nil, err
	}
	return LoadMemoryIndexFor(w)
}

func LoadMemoryIndexFor(w *Workspace) (*MemoryIndex, error) {
	data, err := os.ReadFile(w.MemoryIndexPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &MemoryIndex{Version: memoryIndexVersion, Entries: []IndexEntry{}}, nil
		}
		return nil, err
	}
	data = bytesTrimSpace(data)
	if len(data) == 0 || string(data) == "[]" || string(data) == "null" {
		return &MemoryIndex{Version: memoryIndexVersion, Entries: []IndexEntry{}}, nil
	}
	var idx MemoryIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parse memory index: %w", err)
	}
	if idx.Version == 0 {
		idx.Version = memoryIndexVersion
	}
	if idx.Entries == nil {
		idx.Entries = []IndexEntry{}
	}
	return &idx, nil
}

// SaveMemoryIndex 写入 index.json。
func SaveMemoryIndex(idx *MemoryIndex) error {
	w, err := MustActive()
	if err != nil {
		return err
	}
	return SaveMemoryIndexFor(w, idx)
}

func SaveMemoryIndexFor(w *Workspace, idx *MemoryIndex) error {
	if idx == nil {
		idx = &MemoryIndex{Version: memoryIndexVersion, Entries: []IndexEntry{}}
	}
	idx.Version = memoryIndexVersion
	idx.UpdatedAt = clock.RFC3339()
	canonicalizeIndexEntries(idx)
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(w.MemoryIndexPath()), 0755); err != nil {
		return err
	}
	return os.WriteFile(w.MemoryIndexPath(), data, 0644)
}

// SyncMemoryIndexAfterEvolution 根据本轮演进 touched 文件、learning 与归档路径更新索引。
func SyncMemoryIndexAfterEvolution(touched []string, learning, archivedRel string) error {
	idx, err := LoadMemoryIndex()
	if err != nil {
		return err
	}
	now := clock.RFC3339()
	seen := make(map[string]bool)

	for _, rel := range touched {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" || seen[rel] || strings.HasPrefix(rel, RelMemoryArchive+"/") {
			continue
		}
		if isLegacyLearningFragmentRel(rel) {
			continue
		}
		rel = normalizeModePathRel(rel)
		seen[rel] = true
		entry, ok := indexEntryFromFile(rel, now)
		if !ok {
			continue
		}
		idx.Upsert(entry)
	}

	if arch := strings.TrimSpace(archivedRel); arch != "" && !strings.HasPrefix(filepath.ToSlash(arch), RelMemoryArchive+"/") {
		entry := IndexEntry{
			ID:              indexIDFromSource(arch),
			Source:          arch,
			Summary:         truncateRunes("Archived short-term session", maxIndexSummaryRunes),
			Category:        "episodic",
			Priority:        4,
			DisclosureLevel: "index",
			UpdatedAt:       now,
			Keywords:        []string{"archive", "short-term", "session"},
		}
		if w := Active(); w != nil {
			if b, err := os.ReadFile(w.Path(arch)); err == nil {
				entry.Summary = truncateRunes(firstLineSummary(string(b)), maxIndexSummaryRunes)
				entry.Keywords = extractKeywords(string(b), arch)
			}
		}
		idx.Upsert(entry)
	}

	if learn := strings.TrimSpace(learning); utf8.RuneCountInString(learn) >= 24 {
		if w := Active(); w != nil {
			_ = appendLearningPlaybook(w, learn, now)
			if entry, ok := indexEntryFromFile(RelMemoryLongLearnings, now); ok {
				idx.Upsert(entry)
			}
		}
	}

	idx.Prune(maxIndexEntries)
	return SaveMemoryIndex(idx)
}

// Upsert 按 source 或 id 替换/追加条目。
func (idx *MemoryIndex) Upsert(e IndexEntry) {
	if e.ID == "" {
		e.ID = indexIDFromSource(e.Source)
	}
	if e.UpdatedAt == "" {
		e.UpdatedAt = clock.RFC3339()
	}
	for i := range idx.Entries {
		if idx.Entries[i].ID == e.ID || idx.Entries[i].Source == e.Source {
			idx.Entries[i] = e
			return
		}
	}
	idx.Entries = append(idx.Entries, e)
}

// Prune 保留优先级更高、较新的条目。
func (idx *MemoryIndex) Prune(max int) {
	if max <= 0 || len(idx.Entries) <= max {
		return
	}
	// 简单策略：按 priority 降序再按 updated_at 截断
	entries := idx.Entries
	for i := 0; i < len(entries)-1; i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].Priority > entries[i].Priority {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}
	idx.Entries = entries[:max]
}

// MemoryIndexPromptBlock 注入对话的紧凑索引（不含全文）。
func MemoryIndexPromptBlock(maxBytes int) string {
	return MemoryIndexPromptBlockFor(ActivePromptProfile(), maxBytes)
}

// MemoryIndexPromptBlockFor 按 profile 过滤索引：full 档跳过已在【项目内容】全文注入的 modes/persona.local。
func MemoryIndexPromptBlockFor(p PromptProfile, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = maxIndexPromptBytes
	}
	idx, err := LoadMemoryIndex()
	if err != nil || len(idx.Entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("【Cata 记忆索引】\n\n")
	b.WriteString("> 条目为脑子内文档摘要；`read_file` 用 source 路径（如 `brain/persona.local.md` 或 `brain/memory/long/…`）；磁盘位置见【Cata 路径】块 resolved 映射。\n\n")
	if ProfileRank(p) >= 2 {
		b.WriteString("> full 档：modes/persona.local 已在【项目内容】全文注入，此处不重复列出。\n\n")
	}
	used := 0
	count := 0
	for _, e := range idx.entriesByPriority() {
		if skipIndexEntryForPrompt(e, p) {
			continue
		}
		line := fmt.Sprintf("- [%s] p%d %s — %s\n", e.Category, e.Priority, e.Source, e.Summary)
		if used+len(line) > maxBytes {
			b.WriteString("\n…(index truncated)\n")
			break
		}
		b.WriteString(line)
		used += len(line)
		count++
		if count >= 40 {
			break
		}
	}
	if count == 0 {
		return ""
	}
	return b.String()
}

// isProjectContentFullInjectSource 是否由 terminalProjectContentExcerpt 全文注入（full 档）。
func isProjectContentFullInjectSource(rel string) bool {
	rel = normalizeModePathRel(filepath.ToSlash(strings.TrimSpace(rel)))
	if rel == RelPersonaLocal {
		return true
	}
	if !strings.HasPrefix(rel, DirModes+"/") {
		return false
	}
	switch filepath.Base(rel) {
	case FilePersona, FileBehavior, FileConstraints:
		return true
	default:
		return false
	}
}

func skipIndexEntryForPrompt(e IndexEntry, p PromptProfile) bool {
	if ProfileRank(p) < 2 {
		return false
	}
	return isProjectContentFullInjectSource(e.Source)
}

func canonicalizeIndexEntries(idx *MemoryIndex) {
	if idx == nil || len(idx.Entries) == 0 {
		return
	}
	seen := make(map[string]bool)
	var out []IndexEntry
	for _, e := range idx.Entries {
		e.Source = normalizeModePathRel(filepath.ToSlash(strings.TrimSpace(e.Source)))
		if e.Source == "" || seen[e.Source] {
			continue
		}
		seen[e.Source] = true
		e.ID = indexIDFromSource(e.Source)
		out = append(out, e)
	}
	idx.Entries = out
}

func (idx *MemoryIndex) entriesByPriority() []IndexEntry {
	out := make([]IndexEntry, len(idx.Entries))
	copy(out, idx.Entries)
	for i := 0; i < len(out)-1; i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Priority > out[i].Priority {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func indexEntryFromFile(rel, updatedAt string) (IndexEntry, bool) {
	w := Active()
	if w == nil {
		return IndexEntry{}, false
	}
	abs := w.Path(rel)
	data, err := os.ReadFile(abs)
	if err != nil {
		return IndexEntry{}, false
	}
	body := string(data)
	cat, pri, disc := inferIndexCategory(rel)
	return IndexEntry{
		ID:              indexIDFromSource(rel),
		Source:          rel,
		Summary:         truncateRunes(memorySummaryForIndex(rel, body), maxIndexSummaryRunes),
		Keywords:        extractKeywords(body, rel),
		Category:        cat,
		Priority:        pri,
		DisclosureLevel: disc,
		UpdatedAt:       updatedAt,
	}, true
}

func memorySummaryForIndex(rel, body string) string {
	rel = filepath.ToSlash(rel)
	if rel == RelMemoryLongLearnings {
		if s := recentLearningsExcerptFromBody(body, 3, maxIndexSummaryRunes); s != "" {
			return s
		}
	}
	if rel == RelMemoryLongSessionNotes || strings.HasPrefix(rel, RelMemoryLong+"/") {
		if s := firstContentLineSummary(body); s != "" {
			return s
		}
	}
	return firstLineSummary(body)
}

func recentLearningsExcerptFromBody(body string, maxBullets, maxRunes int) string {
	lines := strings.Split(body, "\n")
	var bullets []string
	for i := len(lines) - 1; i >= 0; i-- {
		ln := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(ln, "- ") && !strings.HasPrefix(ln, "* ") {
			continue
		}
		bullets = append([]string{strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(ln, "- "), "* "))}, bullets...)
		if len(bullets) >= maxBullets {
			break
		}
	}
	if len(bullets) == 0 {
		return ""
	}
	s := strings.Join(bullets, " · ")
	return truncateRunes(s, maxRunes)
}

func firstContentLineSummary(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ">") {
			continue
		}
		return line
	}
	return firstLineSummary(body)
}

func inferIndexCategory(rel string) (category string, priority int, disclosure string) {
	rel = filepath.ToSlash(rel)
	switch {
	case strings.Contains(rel, "/persona.md"):
		return "preference", 9, "index"
	case strings.HasSuffix(rel, "persona.local.md"):
		return "fact", 7, "index"
	case rel == RelMemoryLongLearnings:
		return "procedure", 8, "index"
	case rel == RelMemoryLongSessionNotes:
		return "episodic", 7, "index"
	case strings.HasPrefix(rel, RelMemoryLong+"/consolidated-"):
		return "episodic", 4, "index"
	case strings.HasPrefix(rel, RelMemoryLong+"/"):
		return "fact", 5, "index"
	case strings.Contains(rel, "/behavior.md"):
		return "procedure", 6, "index"
	default:
		return "fact", 4, "index"
	}
}

func indexIDFromSource(source string) string {
	s := strings.TrimSpace(source)
	s = strings.TrimSuffix(s, ".md")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	if s == "" {
		s = "entry"
	}
	return s
}

var reMDHeader = regexp.MustCompile(`(?m)^#{1,3}\s+(.+)$`)

func firstLineSummary(body string) string {
	body = CompactExcessiveNewlines(body)
	for _, m := range reMDHeader.FindAllStringSubmatch(body, 5) {
		if len(m) > 1 {
			t := strings.TrimSpace(m[1])
			if t != "" && !strings.HasPrefix(t, "Current session") {
				return t
			}
		}
	}
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ">") || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return "memory entry"
}

func extractKeywords(body, rel string) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(w string) {
		w = strings.ToLower(strings.TrimSpace(w))
		if len(w) < 2 || seen[w] {
			return
		}
		seen[w] = true
		out = append(out, w)
	}
	for _, p := range strings.FieldsFunc(rel, func(r rune) bool {
		return r == '/' || r == '_' || r == '-' || r == '.'
	}) {
		add(p)
	}
	for _, m := range reMDHeader.FindAllStringSubmatch(body, 8) {
		if len(m) > 1 {
			for _, w := range tokenizeWords(m[1]) {
				add(w)
				if len(out) >= maxIndexKeywords {
					return out
				}
			}
		}
	}
	for _, w := range tokenizeWords(body) {
		add(w)
		if len(out) >= maxIndexKeywords {
			break
		}
	}
	return out
}

func tokenizeWords(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r < 0x4e00
	}) {
		f = strings.TrimSpace(f)
		if utf8.RuneCountInString(f) >= 2 {
			out = append(out, f)
		}
	}
	return out
}

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}
