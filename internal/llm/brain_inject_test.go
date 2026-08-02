package llm

import (
	"strings"
	"testing"

	"cata/internal/cata/brain"
)

func TestWorkerBrainInjectMinimal(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "task body"}}
	out := withBootLeaderSystemMessageFor(msgs, brain.PromptProfileMinimal)
	if len(out) < 3 {
		t.Fatalf("expected boot+brain+user, got %d messages", len(out))
	}
	if out[0].Role != "system" || !strings.Contains(out[0].Content, brain.LoadMinimalBootPrompt()[:8]) {
		t.Fatal("missing minimal boot")
	}
	if !strings.HasPrefix(out[1].Content, brain.TerminalPathsSystemPrefix) {
		t.Fatalf("missing paths block: %q", out[1].Content[:40])
	}
	if strings.Contains(out[1].Content, "delegate_task") {
		t.Fatal("minimal should omit subagent guide")
	}
}

func TestCoalesceSystemMessagesForAPI(t *testing.T) {
	in := []Message{
		{Role: "system", Content: "boot"},
		{Role: "system", Content: "brain"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "yo"},
		{Role: "system", Content: "stray"},
	}
	out := coalesceSystemMessagesForAPI(in)
	if len(out) != 3 {
		t.Fatalf("len=%d want 3: %+v", len(out), out)
	}
	if out[0].Role != "system" || !strings.Contains(out[0].Content, "boot") || !strings.Contains(out[0].Content, "brain") || !strings.Contains(out[0].Content, "stray") {
		t.Fatalf("merged system: %q", out[0].Content)
	}
	if out[1].Role != "user" || out[2].Role != "assistant" {
		t.Fatalf("rest order: %+v", out)
	}
}
