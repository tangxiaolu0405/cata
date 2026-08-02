package evolve

import (
	"testing"

	"cata/internal/cata/brain"
)

func TestFilterUpdatesForDecision_modeEvolve(t *testing.T) {
	dec := &Decision{Action: "mode_evolve", TargetMode: "coder"}
	updates := []DocUpdate{
		{Path: "modes/coder/persona.md", Mode: "append", Content: "prefer small diffs and clear commits here"},
		{Path: "modes/_default/persona.md", Mode: "append", Content: "should not land in coder bucket evolve"},
	}
	out := filterUpdatesForDecision(dec, updates)
	if len(out) != 1 {
		t.Fatalf("got %+v", out)
	}
	ok := false
	for _, u := range out {
		rel, _ := brain.NormalizeEvolveUpdatePath(u.Path)
		if rel == "modes/coder/persona.md" {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("expected modes/coder/persona.md in %+v", out)
	}
}

func TestFilterUpdatesForDecision_consolidateTargetMode(t *testing.T) {
	dec := &Decision{Action: "consolidate", TargetMode: "coder"}
	updates := []DocUpdate{
		{Path: "modes/coder/persona.md", Mode: "append", Content: "consolidate into specialist mode only here"},
		{Path: "memory/long/foo.md", Mode: "append", Content: "memory should be filtered when target_mode set"},
	}
	out := filterUpdatesForDecision(dec, updates)
	if len(out) != 1 {
		t.Fatalf("got %+v", out)
	}
	rel, _ := brain.NormalizeEvolveUpdatePath(out[0].Path)
	if rel != "modes/coder/persona.md" {
		t.Fatalf("got %q", rel)
	}
}

func TestFilterUpdatesForDecision_consolidateDefault(t *testing.T) {
	dec := &Decision{Action: "consolidate"}
	updates := []DocUpdate{
		{Path: "modes/_default/persona.md", Mode: "append", Content: "orch persona notes for default mode"},
		{Path: "memory/long/foo.md", Mode: "append", Content: "long memory detail goes here ok"},
		{Path: "modes/coder/persona.md", Mode: "append", Content: "other mode should pass through on default consolidate"},
	}
	out := filterUpdatesForDecision(dec, updates)
	if len(out) != 3 {
		t.Fatalf("want 3, got %+v", out)
	}
}

func TestFilterUpdatesForDecision_orch(t *testing.T) {
	dec := &Decision{Action: "orch_evolve"}
	updates := []DocUpdate{
		{Path: "modes/coder/persona.md", Mode: "append", Content: "coder stuff should be filtered out completely"},
		{Path: "modes/_default/behavior.md", Mode: "append", Content: "orch scheduling notes go here ok"},
	}
	out := filterUpdatesForDecision(dec, updates)
	if len(out) != 1 {
		t.Fatalf("want 1, got %+v", out)
	}
}

func TestFilterUpdatesForDecision_crystallizeSkill(t *testing.T) {
	dec := &Decision{Action: "crystallize"}
	updates := []DocUpdate{
		{Path: "skills/my-skill/manifest.yaml", Mode: "overwrite", Content: "name: my-skill\nsteps:\n  - run: echo hi"},
		{Path: "modes/_default/persona.md", Mode: "append", Content: "should not land in skill crystallize"},
	}
	out := filterUpdatesForDecision(dec, updates)
	if len(out) != 1 {
		t.Fatalf("got %+v", out)
	}
	rel, _ := brain.NormalizeEvolveUpdatePath(out[0].Path)
	if rel != "skills/my-skill/manifest.yaml" {
		t.Fatalf("got %q", rel)
	}
}

func TestFilterUpdatesForDecision_crystallizeNewMode(t *testing.T) {
	dec := &Decision{Action: "crystallize", NewModeID: "video-editor"}
	updates := []DocUpdate{
		{Path: "modes/video-editor/persona.md", Mode: "overwrite", Content: "I edit project videos with consistent style and pacing"},
		{Path: "skills/foo/manifest.yaml", Mode: "overwrite", Content: "name: foo"},
	}
	out := filterUpdatesForDecision(dec, updates)
	if len(out) != 1 {
		t.Fatalf("got %+v", out)
	}
	rel, _ := brain.NormalizeEvolveUpdatePath(out[0].Path)
	if rel != "modes/video-editor/persona.md" {
		t.Fatalf("got %q", rel)
	}
}

func TestNormalizeDecisionAction_aliases(t *testing.T) {
	if got := normalizeDecisionAction("evolve_mode"); got != "mode_evolve" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeDecisionAction("mode_evolve"); got != "mode_evolve" {
		t.Fatalf("mode_evolve should stay: got %q", got)
	}
	if got := normalizeDecisionAction("consolidate"); got != "consolidate" {
		t.Fatalf("got %q", got)
	}
}
