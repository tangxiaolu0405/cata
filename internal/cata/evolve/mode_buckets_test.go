package evolve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cata/internal/cata/brain"
)

func TestObserveModeBuckets_crystallizeCandidateWithoutDelegate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	root := t.TempDir()
	ws := &brain.Workspace{ID: "ws-crys", RootPath: root, ActiveMode: brain.ModeDefaultID}
	if err := os.MkdirAll(filepath.Join(ws.Dir(), "memory", "short"), 0755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 0; i < 5; i++ {
		b.WriteString("**User:** turn\n\n**Assistant:** did stock fupan work again\n\n")
	}
	body := b.String()
	if len(body) < crystallizeMinShortBytes {
		body = body + strings.Repeat("x", crystallizeMinShortBytes)
	}
	if err := os.WriteFile(ws.ShortTermPath(), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	snap := &Snapshot{ShortTermBytes: int64(len(body))}
	observeModeBuckets(snap, ws)
	found := false
	for _, tr := range snap.Triggers {
		if tr == "crystallize_mode_candidate" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected crystallize_mode_candidate, triggers=%v", snap.Triggers)
	}
}

func TestObserveModeBuckets_crystallizeFromDailyArchives(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	root := t.TempDir()
	ws := &brain.Workspace{ID: "ws-daily", RootPath: root, ActiveMode: brain.ModeDefaultID}
	if err := os.MkdirAll(filepath.Join(ws.Dir(), "memory", "short"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ws.ArchiveDir(), 0755); err != nil {
		t.Fatal(err)
	}
	// 每日一次：当前 short 只有 1 轮，不够「连聊」门禁
	short := "## 2026-07-24T09:00:00+08:00\n\n**User:** 今日复盘\n\n**Assistant:** 做完了\n\n"
	if err := os.WriteFile(ws.ShortTermPath(), []byte(short), 0644); err != nil {
		t.Fatal(err)
	}
	for _, day := range []string{"2026-07-21", "2026-07-22", "2026-07-23"} {
		name := "consolidated-" + day + "-090000.md"
		body := "# Short-term archive\n\n## " + day + "T09:00:00+08:00\n\n**User:** 复盘\n\n**Assistant:** ok\n"
		if err := os.WriteFile(filepath.Join(ws.ArchiveDir(), name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	snap := &Snapshot{ShortTermBytes: int64(len(short))}
	observeModeBuckets(snap, ws)
	found := false
	for _, tr := range snap.Triggers {
		if tr == "crystallize_mode_candidate" {
			found = true
		}
	}
	if !found {
		t.Fatalf("daily archives should trigger crystallize, triggers=%v days=%d", snap.Triggers, collectRecurringJobDays(ws, short))
	}
}

func TestObserveModeBuckets_skipsWhenAlreadyDelegating(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	root := t.TempDir()
	ws := &brain.Workspace{ID: "ws-del", RootPath: root, ActiveMode: brain.ModeDefaultID}
	if err := os.MkdirAll(filepath.Join(ws.Dir(), "memory", "short"), 0755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 0; i < 5; i++ {
		b.WriteString("**User:** turn\n\n**Assistant:** ok\n\n")
	}
	b.WriteString("[delegate_mode mode=fupan case=c1 id=sub-1 status=ok]\n")
	body := b.String() + strings.Repeat("y", crystallizeMinShortBytes)
	if err := os.WriteFile(ws.ShortTermPath(), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	snap := &Snapshot{ShortTermBytes: int64(len(body))}
	observeModeBuckets(snap, ws)
	for _, tr := range snap.Triggers {
		if tr == "crystallize_mode_candidate" {
			t.Fatal("should not crystallize while already delegating")
		}
	}
}
