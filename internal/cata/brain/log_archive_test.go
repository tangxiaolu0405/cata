package brain

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPruneArchivedLogs(t *testing.T) {
	dir := t.TempDir()
	// 12 个归档日志（文件名含 YYYYMMDD-HHMMSS-RRR）。
	for i := 1; i <= 12; i++ {
		name := fmt.Sprintf("llm.202608%02d-000000-000.log", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// 当前活跃日志（非归档，不应被删）。
	if err := os.WriteFile(filepath.Join(dir, "llm.log"), []byte("active"), 0644); err != nil {
		t.Fatal(err)
	}

	pruneArchivedLogs(dir)

	entries, _ := os.ReadDir(dir)
	// 10 个归档 + 1 个活跃 = 11
	if len(entries) != maxArchivedLogs+1 {
		t.Fatalf("expected %d files, got %d", maxArchivedLogs+1, len(entries))
	}
	// 最旧的两个应被删，最新的保留。
	if _, err := os.Stat(filepath.Join(dir, "llm.20260801-000000-000.log")); !os.IsNotExist(err) {
		t.Fatal("oldest archived log should be pruned")
	}
	if _, err := os.Stat(filepath.Join(dir, "llm.20260812-000000-000.log")); err != nil {
		t.Fatal("newest archived log should remain")
	}
	if _, err := os.Stat(filepath.Join(dir, "llm.log")); err != nil {
		t.Fatal("active log should remain")
	}
}
