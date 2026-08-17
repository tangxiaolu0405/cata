package brain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPruneArchiveDir(t *testing.T) {
	t.Setenv("CATA_HOME", t.TempDir())
	w := &Workspace{ID: "prune-ws", ActiveMode: ModeDefaultID}
	dir := w.ArchiveDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	// 放 7 个 consolidated 归档（名字含时间戳，字典序=时间序）。
	for _, name := range []string{
		"consolidated-2026-08-01-000000.md",
		"consolidated-2026-08-02-000000.md",
		"consolidated-2026-08-03-000000.md",
		"consolidated-2026-08-04-000000.md",
		"consolidated-2026-08-05-000000.md",
		"consolidated-2026-08-06-000000.md",
		"consolidated-2026-08-07-000000.md",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := PruneArchiveDir(w, 0); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != maxArchiveFiles {
		t.Fatalf("expected %d files after prune, got %d", maxArchiveFiles, len(entries))
	}
	// 最旧的两个应被删。
	if _, err := os.Stat(filepath.Join(dir, "consolidated-2026-08-01-000000.md")); !os.IsNotExist(err) {
		t.Fatal("oldest archive should be pruned")
	}
	if _, err := os.Stat(filepath.Join(dir, "consolidated-2026-08-07-000000.md")); err != nil {
		t.Fatal("newest archive should remain")
	}
}
