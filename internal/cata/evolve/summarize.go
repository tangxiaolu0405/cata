package evolve

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cata/internal/cata/brain"
)

// summarizeLongTerm merges old long-term memory entries into archive when
// the long-term file count exceeds the threshold.
func summarizeLongTerm(ws *brain.Workspace) ([]string, error) {
	if ws == nil {
		return nil, fmt.Errorf("workspace required")
	}
	longDir := ws.LongTermDir()
	entries, err := os.ReadDir(longDir)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		if brain.IsLongMemoryCanonicalFile(name) {
			continue
		}
		if !brain.IsLongMemoryBulkFile(name) {
			continue
		}
		files = append(files, name)
	}
	if len(files) < longTermSummarizeMinFiles {
		return nil, nil
	}

	sort.Slice(files, func(i, j int) bool {
		pi := filepath.Join(longDir, files[i])
		pj := filepath.Join(longDir, files[j])
		si, _ := os.Stat(pi)
		sj, _ := os.Stat(pj)
		if si == nil || sj == nil {
			return files[i] < files[j]
		}
		return si.ModTime().Before(sj.ModTime())
	})

	count := len(files) / 2
	if count < 1 {
		count = 1
	}
	if count > 12 {
		count = 12
	}

	archiveDir := ws.ArchiveDir()
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return nil, err
	}

	ts := time.Now().Format("20060102-150405")
	var moved []string
	for i := 0; i < count && i < len(files); i++ {
		oldPath := filepath.Join(longDir, files[i])
		base := strings.TrimSuffix(files[i], ".md")
		newName := fmt.Sprintf("%s-%s.md", base, ts)
		newPath := filepath.Join(archiveDir, newName)
		if err := os.Rename(oldPath, newPath); err != nil {
			log.Printf("summarize: move %s → %s: %v", files[i], newName, err)
			continue
		}
		moved = append(moved, newName)
	}

	if len(moved) > 0 {
		log.Printf("summarize: moved %d old entries to archive (%d remain)", len(moved), len(files)-len(moved))
	}
	return moved, nil
}
