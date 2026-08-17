package brain

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"cata/internal/cata/clock"
)

func TestRecordAndEvaluateHits(t *testing.T) {
	t.Setenv("CATA_HOME", t.TempDir())
	w := &Workspace{ID: "ev-ws", ActiveMode: ModeDefaultID}
	if err := os.MkdirAll(filepath.Join(w.Dir(), "memory"), 0755); err != nil {
		t.Fatal(err)
	}
	idx := &MemoryIndex{Version: memoryIndexVersion, Entries: []IndexEntry{
		{ID: "sop", Source: "memory/long/workflow_sop.md", Summary: "sop", Category: "procedure", Priority: 5, UpdatedAt: clock.RFC3339()},
	}}
	if err := SaveMemoryIndexFor(w, idx); err != nil {
		t.Fatal(err)
	}

	// 单周期内命中同一 source 3 次。
	for i := 0; i < 3; i++ {
		if err := RecordRetrievalHits(w, []string{"memory/long/workflow_sop.md"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := EvaluateIndex(w); err != nil {
		t.Fatal(err)
	}

	got, err := LoadMemoryIndexFor(w)
	if err != nil {
		t.Fatal(err)
	}
	e := got.Entries[0]
	if e.Priority != 6 {
		t.Fatalf("priority = %d, want 6 (boosted)", e.Priority)
	}
	if e.Hits != 3 {
		t.Fatalf("hits = %d, want 3", e.Hits)
	}
	// 命中日志评估后应清空。
	if _, err := os.Stat(w.Path(hitsLogName)); !os.IsNotExist(err) {
		t.Fatal("hits log should be cleared after evaluate")
	}
}

func TestEvaluateZombieDemotion(t *testing.T) {
	t.Setenv("CATA_HOME", t.TempDir())
	w := &Workspace{ID: "ev-ws2", ActiveMode: ModeDefaultID}
	if err := os.MkdirAll(filepath.Join(w.Dir(), "memory"), 0755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-90 * 24 * time.Hour).Format(time.RFC3339)
	idx := &MemoryIndex{Version: memoryIndexVersion, Entries: []IndexEntry{
		{ID: "z", Source: "memory/long/old.md", Summary: "old", Category: "fact", Priority: 4, UpdatedAt: old},
	}}
	if err := SaveMemoryIndexFor(w, idx); err != nil {
		t.Fatal(err)
	}
	// 记录一次不相关命中，触发 Evaluate（old 条目未命中且陈旧 → 降权）。
	if err := RecordRetrievalHits(w, []string{"memory/long/other.md"}); err != nil {
		t.Fatal(err)
	}
	if err := EvaluateIndex(w); err != nil {
		t.Fatal(err)
	}
	got, err := LoadMemoryIndexFor(w)
	if err != nil {
		t.Fatal(err)
	}
	if got.Entries[0].Priority != 3 {
		t.Fatalf("zombie priority = %d, want 3 (demoted)", got.Entries[0].Priority)
	}
}

func TestEvaluateNoHitsNoop(t *testing.T) {
	t.Setenv("CATA_HOME", t.TempDir())
	w := &Workspace{ID: "ev-ws3", ActiveMode: ModeDefaultID}
	if err := os.MkdirAll(filepath.Join(w.Dir(), "memory"), 0755); err != nil {
		t.Fatal(err)
	}
	idx := &MemoryIndex{Version: memoryIndexVersion, Entries: []IndexEntry{
		{ID: "a", Source: "memory/long/a.md", Summary: "a", Category: "fact", Priority: 4, UpdatedAt: clock.RFC3339()},
	}}
	if err := SaveMemoryIndexFor(w, idx); err != nil {
		t.Fatal(err)
	}
	// 无命中日志 → Evaluate 应 no-op（不空转写 index）。
	if err := EvaluateIndex(w); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadMemoryIndexFor(w)
	if got.Entries[0].Priority != 4 {
		t.Fatalf("priority changed without hits: %d", got.Entries[0].Priority)
	}
}
