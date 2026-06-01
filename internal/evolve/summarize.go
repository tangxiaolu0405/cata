package evolve

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cata/internal/brain"
)

// summarizeLongTerm merges old long-term memory entries into archive when
// the long-term file count exceeds the threshold.
func summarizeLongTerm(_ *Snapshot) ([]string, error) {
	longDir := brain.LongTermDir()
	entries, err := os.ReadDir(longDir)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			files = append(files, e.Name())
		}
	}
	if len(files) < longTermSummarizeMinFiles {
		return nil, nil
	}

	// Sort by modification time, oldest first
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

	// Move oldest half to archive
	count := len(files) / 2
	if count < 1 {
		count = 1
	}
	if count > 12 {
		count = 12
	}

	archiveDir := brain.ArchiveDir()
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

// shouldSummarizeLongTerm returns true when long-term file count exceeds threshold.
func shouldSummarizeLongTerm(snap *Snapshot) bool {
	return snap.LongTermFileCount >= longTermSummarizeMinFiles
}
