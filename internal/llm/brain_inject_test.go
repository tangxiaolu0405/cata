package llm

import (
	"strings"
	"testing"

	"cata/internal/brain"
)

func TestWorkerBrainInjectMinimal(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "task body"}}
	out := withBootLeaderSystemMessageFor(msgs, brain.PromptProfileMinimal)
	if len(out) < 3 {
		t.Fatalf("expected boot+brain+user, got %d messages", len(out))
	}
	if out[0].Role != "system" || !strings.Contains(out[0].Content, brain.MinimalBootPrompt[:20]) {
		t.Fatal("missing minimal boot")
	}
	if !strings.HasPrefix(out[1].Content, brain.TerminalPathsSystemPrefix) {
		t.Fatalf("missing paths block: %q", out[1].Content[:40])
	}
	if strings.Contains(out[1].Content, "delegate_task") {
		t.Fatal("minimal should omit subagent guide")
	}
}
