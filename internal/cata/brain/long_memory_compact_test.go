package brain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsLearningNoise(t *testing.T) {
	if !isLearningNoise("No learning as no new facts.") {
		t.Fatal("expected noise")
	}
	if !isLearningNoise("Periodic consolidation with no new information") {
		t.Fatal("expected noise")
	}
	if isLearningNoise("用户明确要求抓取用Windows浏览器、保存用WSL bash heredoc") {
		t.Fatal("expected signal")
	}
}

func TestCompactLearningPlaybookContent_dedupesAndDropsNoise(t *testing.T) {
	raw := `# Evolution learnings

## 2026-05-20T15:05:05

- User prefers CSV output to D:\stock

## 2026-05-20T15:09:39

- Periodic consolidation triggered with no new short-term facts

## 2026-05-20T15:10:06

- User prefers CSV output to D:\stock

## 2026-05-21T16:52:11

- Session captured limit-up analysis with local export to D:\stock
`
	out := compactLearningPlaybookContent(raw)
	if strings.Contains(out, "Periodic consolidation") {
		t.Fatalf("noise not removed: %q", out)
	}
	if strings.Count(out, "User prefers CSV") > 1 {
		t.Fatalf("duplicate not merged: %q", out)
	}
	if !strings.Contains(out, "limit-up analysis") {
		t.Fatalf("signal missing: %q", out)
	}
}

func TestCompactLongMemory_archivesBulk(t *testing.T) {
	ws, _ := testWorkspace(t, "compact-ws")
	longDir := ws.LongTermDir()
	if err := os.MkdirAll(longDir, 0755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(longDir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("consolidated-2026-06-30-160757.md", "# archive me\n")
	write("2026-07-10-summary.md", "## summary\n\n- fact\n")
	write("workflow_sop.md", "## sop\n")
	write("learnings.md", "# Evolution learnings\n\n## 2026-07-01\n\n- 用户要求保留星星优先级格式输出\n")
	if err := os.MkdirAll(filepath.Dir(ws.MemoryIndexPath()), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ws.MemoryIndexPath(), []byte(`{"version":1,"entries":[{"id":"c1","source":"memory/long/consolidated-2026-06-30-160757.md","summary":"x","category":"episodic","priority":4}]}`), 0644); err != nil {
		t.Fatal(err)
	}

	SetActive(ws)
	defer SetActive(nil)
	if err := CompactLongMemoryFor(ws); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(longDir, "consolidated-2026-06-30-160757.md")); !os.IsNotExist(err) {
		t.Fatal("consolidated should be archived")
	}
	if _, err := os.Stat(ws.Path(RelMemoryLongSessionNotes)); err != nil {
		t.Fatalf("session-notes missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(longDir, "workflow_sop.md")); err != nil {
		t.Fatal("workflow_sop should remain")
	}
	entries, _ := os.ReadDir(longDir)
	if len(entries) > 6 {
		t.Fatalf("too many files left in long/: %d", len(entries))
	}
}
