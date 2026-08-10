package brain

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"cata/internal/cata/clock"
)

const (
	maxLearningBulletRunes     = 280
	maxLearningPlaybookBullets = 80
	minLearningBulletRunes     = 12
	longMemoryBulkArchiveDir   = "long-bulk"
)

var learningNoiseSubstrings = []string{
	"no learning",
	"no new facts",
	"no new patterns",
	"no new information",
	"nothing to consolidate",
	"maintenance log appended",
	"periodic consolidation",
	"no-op",
	"idle cycle",
}

// LongMemoryCanonicalFiles 保留在 memory/long 根目录的活文件（其余 bulk 归档）。
func LongMemoryCanonicalFiles() []string {
	return []string{
		"learnings.md",
		"workflow_sop.md",
		"sub-agent-failures.md",
		"session-notes.md",
	}
}

func isLongMemoryCanonicalName(name string) bool {
	return IsLongMemoryCanonicalFile(name)
}

// IsLongMemoryCanonicalFile 是否应保留在 memory/long 根目录的活文件。
func IsLongMemoryCanonicalFile(name string) bool {
	for _, c := range LongMemoryCanonicalFiles() {
		if name == c {
			return true
		}
	}
	return false
}

func isLongMemoryBulkName(name string) bool {
	return IsLongMemoryBulkFile(name)
}

// IsLongMemoryBulkFile 是否应归档的 bulk（consolidated-*、*-summary、*-session）。
func IsLongMemoryBulkFile(name string) bool {
	if IsLongMemoryCanonicalFile(name) {
		return false
	}
	if strings.HasPrefix(name, "consolidated-") && strings.HasSuffix(name, ".md") {
		return true
	}
	if strings.HasSuffix(name, "-summary.md") {
		return true
	}
	if strings.HasSuffix(name, "-session.md") {
		return true
	}
	return false
}

// MigrateAllLongMemoryCompact 压缩 playbook + 归档 long 目录 bulk 文件（各 workspace 一次性）。
func MigrateAllLongMemoryCompact() {
	seen := make(map[string]bool)
	prev := Active()
	defer SetActive(prev)

	list, _ := ListWorkspaces()
	for _, ws := range list {
		if ws == nil || seen[ws.ID] {
			continue
		}
		seen[ws.ID] = true
		SetActive(ws)
		if err := CompactLongMemoryFor(ws); err != nil {
			log.Printf("long memory compact [%s]: %v", ws.ID, err)
		}
	}
	entries, err := os.ReadDir(workspacesRoot())
	if err != nil {
		return
	}
	for _, ent := range entries {
		if !ent.IsDir() || seen[ent.Name()] {
			continue
		}
		ws := workspaceFromDiskID(ent.Name())
		if ws == nil {
			continue
		}
		SetActive(ws)
		if err := CompactLongMemoryFor(ws); err != nil {
			log.Printf("long memory compact [%s]: %v", ws.ID, err)
		}
	}
}

// CompactLongMemoryFor 压缩 learnings.md、合并 session 摘要、归档 consolidated-* 等 bulk。
func CompactLongMemoryFor(w *Workspace) error {
	if w == nil {
		return fmt.Errorf("workspace required")
	}
	marker := w.Path(filepath.Join("memory", fileLongMemoryCompactV1))
	if _, err := os.Stat(marker); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	longDir := w.LongTermDir()
	if err := os.MkdirAll(longDir, 0755); err != nil {
		return err
	}
	if err := maintainLongMemorySteps(w); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0755); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte(clock.RFC3339()+"\n"), 0644)
}

// MaintainLongMemoryAfterEvolution 每轮 evolve 后维护 memory/long：搬 stray bulk、压 playbook、刷新索引。
func MaintainLongMemoryAfterEvolution(w *Workspace) error {
	if w == nil {
		return nil
	}
	return maintainLongMemorySteps(w)
}

func maintainLongMemorySteps(w *Workspace) error {
	longDir := w.LongTermDir()
	if err := os.MkdirAll(longDir, 0755); err != nil {
		return err
	}
	if err := mergeSessionNoteFiles(w, longDir); err != nil {
		return err
	}
	if err := archiveLongMemoryBulkFiles(w, longDir); err != nil {
		return err
	}
	if err := compactLearningPlaybookFile(w.Path(RelMemoryLongLearnings)); err != nil {
		return err
	}
	return refreshIndexAfterLongCompact(w)
}

func mergeSessionNoteFiles(w *Workspace, longDir string) error {
	entries, err := os.ReadDir(longDir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasSuffix(n, "-summary.md") || strings.HasSuffix(n, "-session.md") {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	dest := w.Path(RelMemoryLongSessionNotes)
	var b strings.Builder
	b.WriteString("# Session notes (merged)\n\n> 日/会话摘要合并；原文在 memory/archive/" + longMemoryBulkArchiveDir + "/\n")
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(longDir, name))
		if err != nil {
			continue
		}
		body := strings.TrimSpace(CompactExcessiveNewlines(string(data)))
		if body == "" {
			continue
		}
		b.WriteString("\n\n## ")
		b.WriteString(strings.TrimSuffix(name, ".md"))
		b.WriteString("\n\n")
		if len(body) > 2400 {
			body = truncateRunes(body, 2400) + "\n…(truncated)"
		}
		b.WriteString(body)
		b.WriteString("\n")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	return os.WriteFile(dest, []byte(b.String()), 0644)
}

func archiveLongMemoryBulkFiles(w *Workspace, longDir string) error {
	entries, err := os.ReadDir(longDir)
	if err != nil {
		return err
	}
	var bulk []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if isLongMemoryBulkName(e.Name()) {
			bulk = append(bulk, e.Name())
		}
	}
	if len(bulk) == 0 {
		return nil
	}
	ts := clock.Format("20060102-150405")
	archiveDir := w.Path(filepath.Join(RelMemoryArchive, longMemoryBulkArchiveDir+"-"+ts))
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return err
	}
	for _, name := range bulk {
		src := filepath.Join(longDir, name)
		dst := filepath.Join(archiveDir, name)
		if err := os.Rename(src, dst); err != nil {
			log.Printf("long compact: archive %s: %v", name, err)
		}
	}
	return nil
}

func compactLearningPlaybookFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	out := compactLearningPlaybookContent(string(data))
	if strings.TrimSpace(out) == "" {
		return nil
	}
	return os.WriteFile(path, []byte(out), 0644)
}

func compactLearningPlaybookContent(raw string) string {
	raw = CompactExcessiveNewlines(raw)
	sections := splitPlaybookSections(raw)
	type dayBucket struct {
		when    string
		bullets []string
	}
	seen := make(map[string]bool)
	var buckets []dayBucket
	bucketIndex := make(map[string]int)

	addBullet := func(when, bullet string) {
		bullet = strings.TrimSpace(bullet)
		if isLearningNoise(bullet) {
			return
		}
		bullet = truncateRunes(bullet, maxLearningBulletRunes)
		key := normalizeLearningKey(bullet)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		day := playbookDayKey(when)
		i, ok := bucketIndex[day]
		if !ok {
			buckets = append(buckets, dayBucket{when: day})
			i = len(buckets) - 1
			bucketIndex[day] = i
		}
		buckets[i].bullets = append(buckets[i].bullets, bullet)
	}

	for _, sec := range sections {
		for _, line := range strings.Split(sec.body, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "- ") {
				continue
			}
			addBullet(sec.title, strings.TrimPrefix(line, "- "))
		}
	}

	// 保留最近若干条：从尾部 day bucket 取 bullet，总数上限
	var flat []struct{ when, bullet string }
	for i := len(buckets) - 1; i >= 0; i-- {
		for j := len(buckets[i].bullets) - 1; j >= 0; j-- {
			flat = append(flat, struct{ when, bullet string }{buckets[i].when, buckets[i].bullets[j]})
			if len(flat) >= maxLearningPlaybookBullets {
				goto build
			}
		}
	}
build:
	if len(flat) == 0 {
		return strings.TrimSpace(learningPlaybookHeader) + "\n"
	}
	// 反转为时间正序
	for i, j := 0, len(flat)-1; i < j; i, j = i+1, j-1 {
		flat[i], flat[j] = flat[j], flat[i]
	}

	var b strings.Builder
	b.WriteString(strings.TrimSpace(learningPlaybookHeader))
	b.WriteString("\n\n> Compacted: noise removed, duplicates merged, bullets capped.\n")
	curDay := ""
	for _, item := range flat {
		if item.when != curDay {
			curDay = item.when
			b.WriteString("\n\n## ")
			b.WriteString(curDay)
			b.WriteString("\n")
		}
		b.WriteString("\n- ")
		b.WriteString(item.bullet)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()) + "\n"
}

type playbookSec struct {
	title string
	body  string
}

func splitPlaybookSections(raw string) []playbookSec {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	// 去掉顶层标题块
	if idx := strings.Index(raw, "\n## "); idx >= 0 {
		raw = raw[idx+1:]
	} else {
		return nil
	}
	parts := strings.Split(raw, "\n## ")
	var out []playbookSec
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		lines := strings.SplitN(p, "\n", 2)
		title := strings.TrimSpace(lines[0])
		body := ""
		if len(lines) > 1 {
			body = lines[1]
		}
		if strings.EqualFold(title, "Migrated fragments") {
			title = "migrated"
		}
		out = append(out, playbookSec{title: title, body: body})
	}
	return out
}

func playbookDayKey(when string) string {
	when = strings.TrimSpace(when)
	if len(when) >= 10 && when[4] == '-' && when[7] == '-' {
		return when[:10]
	}
	// 2006-01-02T15:04:05
	if len(when) >= 10 {
		return when[:10]
	}
	return when
}

func isLearningNoise(s string) bool {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) < minLearningBulletRunes {
		return true
	}
	lower := strings.ToLower(s)
	for _, sub := range learningNoiseSubstrings {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	// 纯标题残片
	if strings.HasPrefix(s, "# ") && !strings.Contains(s, "\n-") {
		if utf8.RuneCountInString(s) < 80 {
			return true
		}
	}
	return false
}

func normalizeLearningKey(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r >= 0x4e00 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func refreshIndexAfterLongCompact(w *Workspace) error {
	idx, err := LoadMemoryIndexFor(w)
	if err != nil {
		return err
	}
	now := clock.RFC3339()
	var kept []IndexEntry
	for _, e := range idx.Entries {
		src := filepath.ToSlash(e.Source)
		if isLegacyLearningFragmentRel(src) {
			continue
		}
		base := filepath.Base(src)
		if strings.HasPrefix(base, "consolidated-") {
			continue
		}
		if strings.HasSuffix(base, "-summary.md") || strings.HasSuffix(base, "-session.md") {
			continue
		}
		if strings.HasPrefix(src, RelMemoryArchive+"/") {
			continue
		}
		kept = append(kept, e)
	}
	idx.Entries = kept
	for _, rel := range []string{
		RelMemoryLongLearnings,
		RelMemoryLongSessionNotes,
		RelMemoryLong + "/workflow_sop.md",
		RelMemoryLong + "/sub-agent-failures.md",
	} {
		if _, err := os.Stat(w.Path(rel)); err != nil {
			continue
		}
		if entry, ok := indexEntryFromWorkspace(w, rel, now); ok {
			idx.Upsert(entry)
		}
	}
	idx.Prune(maxIndexEntries)
	return SaveMemoryIndexFor(w, idx)
}
