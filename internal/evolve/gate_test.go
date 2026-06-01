package evolve

import (
	"testing"
	"time"
)

func TestShouldInvokeLLM_noTriggers(t *testing.T) {
	snap := &Snapshot{ShortTermBytes: 100, ArchiveFileCount: 1}
	computeTriggers(snap, nil)
	ok, _ := shouldInvokeLLM(snap, time.Time{}, "")
	if ok {
		t.Fatal("expected skip when no triggers")
	}
}

func TestShouldInvokeLLM_shortTermTrigger(t *testing.T) {
	snap := &Snapshot{ShortTermBytes: shortTermTriggerBytes, ShortTermModTime: "2026-01-01T00:00:00Z"}
	computeTriggers(snap, nil)
	ok, reason := shouldInvokeLLM(snap, time.Time{}, "other")
	if !ok {
		t.Fatalf("expected invoke: %s", reason)
	}
}

func TestShouldInvokeLLM_unchangedFingerprint(t *testing.T) {
	snap := &Snapshot{
		ShortTermBytes:   shortTermTriggerBytes,
		ShortTermModTime: "2026-01-01T00:00:00Z",
	}
	computeTriggers(snap, nil)
	fp := snap.Fingerprint()
	ok, _ := shouldInvokeLLM(snap, time.Time{}, fp)
	if ok {
		t.Fatal("expected skip for same fingerprint")
	}
}

func TestShouldInvokeLLM_shortUpdatedSinceEvolution(t *testing.T) {
	snap := &Snapshot{
		ShortTermBytes:      800,
		ShortTermModTime:    "2026-05-18T12:00:00Z",
		LastEvolutionAt:     "2026-05-18T11:00:00Z",
		LastEvolutionAction: "consolidate",
	}
	computeTriggers(snap, nil)
	ok, reason := shouldInvokeLLM(snap, time.Time{}, "")
	if !ok {
		t.Fatalf("expected invoke for updated short-term: %s", reason)
	}
}

func TestFilterUpdates_dropsTiny(t *testing.T) {
	out := filterUpdates([]DocUpdate{{Path: "hot.md", Mode: "append", Content: "ok"}})
	if len(out) != 0 {
		t.Fatalf("expected drop short patch, got %d", len(out))
	}
}
