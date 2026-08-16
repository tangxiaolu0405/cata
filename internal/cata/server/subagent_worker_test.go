package server

import (
	"strings"
	"testing"

	"cata/internal/llm"
)

func TestBuildWorkerSystemPrompt(t *testing.T) {
	p := buildWorkerSystemPromptFor("Run go test ./pkg/foo", "module root: ./pkg/foo", "", nil)
	// worker 身份与 STATUS 协议由角色卡片（system）提供，此处只拼动态任务上下文。
	if strings.Contains(p, "STATUS:") {
		t.Fatalf("worker contract should live in role card, not user prompt: %q", p)
	}
	if !strings.Contains(p, "module root") || !strings.Contains(p, "go test") {
		t.Fatalf("missing task/context: %q", p)
	}
}

func TestWorkerRoleCardHasContract(t *testing.T) {
	card, err := llm.CardForRole(llm.RoleWorker)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card.Body, "STATUS:") || !strings.Contains(card.Body, "worker") {
		t.Fatalf("worker role card should carry the contract: %q", card.Body)
	}
}

func TestFilterWorkerTools(t *testing.T) {
	all := []llm.Tool{
		{Function: llm.ToolFunction{Name: "read_file"}},
		{Function: llm.ToolFunction{Name: "run_command"}},
		{Function: llm.ToolFunction{Name: "browser_navigate"}},
	}
	got, err := filterWorkerTools(all, []string{"read_file", "browser_navigate"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	_, err = filterWorkerTools(all, []string{"missing"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestTruncateWorkerToolResult(t *testing.T) {
	in := strings.Repeat("a", 100)
	out := truncateWorkerToolResult(in, 40)
	if len(out) >= 100 || !strings.Contains(out, "truncated") {
		t.Fatalf("got len=%d", len(out))
	}
}
