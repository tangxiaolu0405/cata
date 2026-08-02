package brain

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"cata/internal/cata/clock"
)

const (
	learningPlaybookHeader = "# Evolution learnings\n\n> 滚动账本（仅 long-term）：每轮 evolve 的 learning 审计摘要。可复用事实须经 updates[] 写入 persona/behavior/persona.local 或定向 memory/long/*.md——勿指望本文件当 SOP。\n"
	maxLearningPlaybookBytes = 96 * 1024
)

// MigrateAllLearningFragments 对所有已注册 workspace 及磁盘上存在的脑子格执行一次性迁移。
func MigrateAllLearningFragments() {
	seen := make(map[string]bool)
	prev := Active()
	defer SetActive(prev)

	list, err := ListWorkspaces()
	if err != nil {
		log.Printf("learning migrate: list workspaces: %v", err)
	}
	for _, ws := range list {
		if ws == nil || seen[ws.ID] {
			continue
		}
		seen[ws.ID] = true
		SetActive(ws)
		if err := MigrateLearningFragmentsFor(ws); err != nil {
			log.Printf("learning migrate [%s]: %v", ws.ID, err)
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
		if err := MigrateLearningFragmentsFor(ws); err != nil {
			log.Printf("learning migrate [%s]: %v", ws.ID, err)
		}
	}
}

func workspaceFromDiskID(id string) *Workspace {
	dir := filepath.Join(workspacesRoot(), id)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil
	}
	ws := &Workspace{ID: id, ActiveMode: ModeDefaultID}
	metaPath := filepath.Join(dir, RelMetaJSON)
	if data, err := os.ReadFile(metaPath); err == nil {
		var m struct {
			RootPath   string `json:"root_path"`
			ActiveMode string `json:"active_mode"`
		}
		if json.Unmarshal(data, &m) == nil {
			ws.RootPath = m.RootPath
			if m.ActiveMode != "" {
				ws.ActiveMode = NormalizeModeID(m.ActiveMode)
			}
		}
	}
	return ws
}

// MigrateLearningFragmentsFor 合并 memory/long/learnings/learning-*.md → memory/long/learnings.md，归档碎片并清理 index。
func MigrateLearningFragmentsFor(w *Workspace) error {
	if w == nil {
		return fmt.Errorf("workspace required")
	}
	marker := w.Path(filepath.Join("memory", fileLearningPlaybookMigrate))
	if _, err := os.Stat(marker); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	fragDir := w.Path(filepath.Join(RelMemoryLong, "learnings"))
	playbookRel := RelMemoryLongLearnings
	playbookAbs := w.Path(playbookRel)

	var sections []playbookSection
	var names []string
	if entries, err := os.ReadDir(fragDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			if !strings.HasPrefix(e.Name(), "learning-") {
				continue
			}
			names = append(names, e.Name())
		}
		sort.Strings(names)
		for _, name := range names {
			data, err := os.ReadFile(filepath.Join(fragDir, name))
			if err != nil {
				continue
			}
			body := extractLearningBody(string(data))
			if strings.TrimSpace(body) == "" {
				continue
			}
			sections = append(sections, playbookSection{
				When:   learningTimestampFromName(name),
				Bullet: body,
			})
		}
	}

	if len(sections) > 0 {
		if err := mergePlaybookSections(playbookAbs, sections, true); err != nil {
			return err
		}
		if err := archiveLearningFragments(w, fragDir, names); err != nil {
			return err
		}
	}

	if err := pruneIndexLearningFragmentsFor(w, playbookRel); err != nil {
		return err
	}
	// 碎片合并后立即压缩（去噪+去重），不依赖 long_memory_compact 标记
	_ = compactLearningPlaybookFile(playbookAbs)

	if err := os.MkdirAll(filepath.Dir(marker), 0755); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte(clock.RFC3339()+"\n"), 0644)
}

type playbookSection struct {
	When   string
	Bullet string
}

func appendLearningPlaybook(w *Workspace, learning, when string) error {
	if w == nil {
		return fmt.Errorf("workspace required")
	}
	learning = strings.TrimSpace(learning)
	if utf8.RuneCountInString(learning) < 24 {
		return nil
	}
	if isLearningNoise(learning) {
		return nil
	}
	if when == "" {
		when = clock.RFC3339()
	}
	abs := w.Path(RelMemoryLongLearnings)
	if err := mergePlaybookSections(abs, []playbookSection{{When: when, Bullet: learning}}, false); err != nil {
		return err
	}
	if err := trimPlaybookIfNeeded(abs, maxLearningPlaybookBytes); err != nil {
		return err
	}
	return compactLearningPlaybookFile(abs)
}

func mergePlaybookSections(playbookAbs string, sections []playbookSection, migrated bool) error {
	if len(sections) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(playbookAbs), 0755); err != nil {
		return err
	}
	var b strings.Builder
	if data, err := os.ReadFile(playbookAbs); err == nil && strings.TrimSpace(string(data)) != "" {
		b.WriteString(strings.TrimSpace(string(data)))
	} else {
		b.WriteString(strings.TrimSpace(learningPlaybookHeader))
	}
	if migrated {
		b.WriteString("\n\n## Migrated fragments\n")
	}
	for _, sec := range sections {
		b.WriteString("\n\n## ")
		b.WriteString(sec.When)
		b.WriteString("\n\n- ")
		b.WriteString(strings.TrimSpace(sec.Bullet))
		b.WriteString("\n")
	}
	return os.WriteFile(playbookAbs, []byte(CompactExcessiveNewlines(b.String())+"\n"), 0644)
}

func extractLearningBody(raw string) string {
	raw = CompactExcessiveNewlines(raw)
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "# Evolution learning")
	raw = strings.TrimSpace(raw)
	return raw
}

func learningTimestampFromName(name string) string {
	base := strings.TrimSuffix(name, ".md")
	base = strings.TrimPrefix(base, "learning-")
	if len(base) >= 15 {
		// 20060102-150405 → 2006-01-02T15:04:05
		return fmt.Sprintf("%s-%s-%sT%s:%s:%s",
			base[0:4], base[4:6], base[6:8],
			base[9:11], base[11:13], base[13:15])
	}
	return base
}

func archiveLearningFragments(w *Workspace, fragDir string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	ts := clock.Format("20060102-150405")
	archiveDir := w.Path(filepath.Join(RelMemoryArchive, "learnings-fragments-"+ts))
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return err
	}
	for _, name := range names {
		if !strings.HasPrefix(name, "learning-") || !strings.HasSuffix(name, ".md") {
			continue
		}
		src := filepath.Join(fragDir, name)
		dst := filepath.Join(archiveDir, name)
		if err := os.Rename(src, dst); err != nil {
			log.Printf("learning migrate: archive %s: %v", name, err)
		}
	}
	// 移除空目录
	_ = os.Remove(fragDir)
	return nil
}

func pruneIndexLearningFragmentsFor(w *Workspace, playbookRel string) error {
	idx, err := LoadMemoryIndexFor(w)
	if err != nil {
		return err
	}
	prefix := RelMemoryLong + "/learnings/learning-"
	var kept []IndexEntry
	for _, e := range idx.Entries {
		src := filepath.ToSlash(e.Source)
		if strings.HasPrefix(src, prefix) {
			continue
		}
		kept = append(kept, e)
	}
	idx.Entries = kept
	if _, err := os.Stat(w.Path(playbookRel)); err == nil {
		if entry, ok := indexEntryFromWorkspace(w, playbookRel, clock.RFC3339()); ok {
			idx.Upsert(entry)
		}
	}
	idx.Prune(maxIndexEntries)
	return SaveMemoryIndexFor(w, idx)
}

func indexEntryFromWorkspace(w *Workspace, rel, updatedAt string) (IndexEntry, bool) {
	prev := Active()
	SetActive(w)
	defer SetActive(prev)
	return indexEntryFromFile(rel, updatedAt)
}

func isLegacyLearningFragmentRel(rel string) bool {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	return strings.HasPrefix(rel, RelMemoryLong+"/learnings/learning-")
}

func trimPlaybookIfNeeded(path string, max int) error {
	data, err := os.ReadFile(path)
	if err != nil || len(data) <= max {
		return err
	}
	s := string(data)
	cut := len(s) - max
	if cut < 0 {
		return nil
	}
	// 从头部裁掉最旧内容，保留 playbook 头与尾部
	if idx := strings.Index(s[cut:], "\n## "); idx >= 0 {
		s = learningPlaybookHeader + "\n…(older entries trimmed)\n\n" + strings.TrimSpace(s[cut+idx+1:])
	} else {
		s = truncateRunes(s, max/4) // fallback
	}
	return os.WriteFile(path, []byte(s), 0644)
}
