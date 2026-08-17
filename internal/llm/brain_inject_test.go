package llm

import (
	"context"
	"strings"
	"testing"

	"cata/internal/cata/brain"
)

func TestWorkerBrainInjectMinimal(t *testing.T) {
	t.Setenv("CATA_HOME", t.TempDir())
	c := &Client{}
	if err := c.attachRoleCard(RoleWorker); err != nil {
		t.Fatal(err)
	}
	msgs := []Message{{Role: "user", Content: "task body"}}
	out := c.assembleSystemForRole(context.Background(), msgs, brain.PromptProfileMinimal)
	if len(out) < 3 {
		t.Fatalf("expected identity+brain+user, got %d messages", len(out))
	}
	// out[0] = worker 角色卡片身份（含 STATUS 协议），out[1] = minimal brain 节选（仅路径块）。
	if out[0].Role != "system" || !strings.Contains(out[0].Content, "STATUS:") {
		t.Fatal("missing worker identity (STATUS protocol)")
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
