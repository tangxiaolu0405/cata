package server

import (
	"context"
	"strings"
	"testing"

	"cata/internal/cata/secrets"
	"cata/internal/llm"
)

func TestPartitionChatToolBatches(t *testing.T) {
	calls := []llm.ToolCall{
		{ID: "1", Function: llm.ToolCallFunction{Name: "read_file"}},
		{ID: "2", Function: llm.ToolCallFunction{Name: "list_files"}},
		{ID: "3", Function: llm.ToolCallFunction{Name: "run_command"}},
		{ID: "4", Function: llm.ToolCallFunction{Name: "delegate_task"}},
		{ID: "5", Function: llm.ToolCallFunction{Name: "read_file"}},
	}
	batches := partitionChatToolBatches(calls)
	if len(batches) != 3 {
		t.Fatalf("batches=%d want 3", len(batches))
	}
	if !batches[0].parallel || len(batches[0].calls) != 2 {
		t.Fatalf("batch0: parallel=%v len=%d", batches[0].parallel, len(batches[0].calls))
	}
	if batches[1].parallel || batches[1].calls[0].Function.Name != "run_command" {
		t.Fatalf("batch1: %+v", batches[1])
	}
	if !batches[2].parallel || len(batches[2].calls) != 2 {
		t.Fatalf("batch2: parallel=%v len=%d", batches[2].parallel, len(batches[2].calls))
	}
}

func TestChatToolParallelSafe(t *testing.T) {
	for _, name := range []string{"read_file", "read_skill", "list_files", "delegate_task"} {
		if !chatToolParallelSafe(name) {
			t.Fatalf("%s should be parallel", name)
		}
	}
	for _, name := range []string{"run_command", "ask_user", "delegate_wait", "search_replace"} {
		if chatToolParallelSafe(name) {
			t.Fatalf("%s should be sequential", name)
		}
	}
}

func TestPartitionSingleParallelTool(t *testing.T) {
	calls := []llm.ToolCall{{ID: "1", Function: llm.ToolCallFunction{Name: "read_file"}}}
	batches := partitionChatToolBatches(calls)
	if len(batches) != 1 || len(batches[0].calls) != 1 {
		t.Fatalf("single read: %+v", batches)
	}
}

func TestManageMCPToolSequential(t *testing.T) {
	if chatToolParallelSafe("manage_mcp") {
		t.Fatal("manage_mcp should be sequential (mutates config + reloads MCP)")
	}
}

// TestAppendChatToolResultRedacts 验证工具结果进入 history 前已知 secret 被掩盖。
func TestAppendChatToolResultRedacts(t *testing.T) {
	// 构造一个含已知 secret 的脱敏器（不依赖启动时的真实收集）。
	old := serverRedactor
	serverRedactor = secrets.New(4)
	serverRedactor.Add("sk-live-secret-2024")
	defer func() { serverRedactor = old }()

	ss := &SocketServer{}
	hist := []llm.Message{}
	res := chatToolExecResult{
		tc:   llm.ToolCall{ID: "t1", Function: llm.ToolCallFunction{Name: "run_command"}},
		out:  "cat ~/.config: sk-live-secret-2024 leaked HERE",
		name: "run_command",
	}
	ss.appendChatToolResult(&hist, res)
	if len(hist) != 1 {
		t.Fatalf("history len=%d", len(hist))
	}
	content := hist[0].Content
	if containsStr(content, "sk-live-secret-2024") {
		t.Fatalf("secret leaked into history: %q", content)
	}
	if !containsStr(content, "***REDACTED***") {
		t.Fatalf("expected redaction placeholder, got %q", content)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestExtractResolvedPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"create_file a.txt resolved=/abs/a.txt: wrote 12 bytes", "/abs/a.txt"},
		{"append_file x resolved=/p/x.md: appended 5 bytes (was 0)", "/p/x.md"},
		{"search_replace f resolved=/p/f.go: 1 replacement(s), 10 -> 20 bytes", "/p/f.go"},
		{"no resolved here", ""},
		{"resolved=", ""},
	}
	for _, c := range cases {
		if got := extractResolvedPath(c.in); got != c.want {
			t.Fatalf("extractResolvedPath(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestExtractWrittenBytes(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"create_file a resolved=/a: wrote 12 bytes", 12, true},
		{"append_file x resolved=/x: appended 5 bytes (was 0)", 5, true},
		{"search_replace f resolved=/f: 1 replacement(s), 10 -> 20 bytes", 20, true},
		{"nothing", 0, false},
	}
	for _, c := range cases {
		got, ok := extractWrittenBytes(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Fatalf("extractWrittenBytes(%q)=%d,%v want %d,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// TestUnifiedDiff 验证统一 diff 生成：改动内容正确、相同内容返回空。
func TestUnifiedDiff(t *testing.T) {
	d, err := unifiedDiff("f.go", "line1\nline2\n", "line1\nline2-changed\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d, "line2") || !strings.Contains(d, "line2-changed") {
		t.Fatalf("diff=%q", d)
	}
	if !strings.Contains(d, "--- f.go") || !strings.Contains(d, "+++ f.go") {
		t.Fatalf("missing diff headers: %q", d)
	}
	// 相同内容 → 空 diff。
	same, err := unifiedDiff("f.go", "a\nb\n", "a\nb\n")
	if err != nil || same != "" {
		t.Fatalf("same content diff=%q err=%v", same, err)
	}
}

// TestFileDiffSinkIdempotent 验证 ctx diff sink：写入一次生效、重复不覆盖（幂等）。
func TestFileDiffSinkIdempotent(t *testing.T) {
	var sink string
	ctx := withFileDiffSink(context.Background(), &sink)
	emitFileDiff(ctx, "/a", "old\n", "new\n")
	if !strings.Contains(sink, "new") {
		t.Fatalf("sink not filled: %q", sink)
	}
	// 第二次（不同内容）不再覆盖。
	emitFileDiff(ctx, "/a", "x\n", "y\n")
	if strings.Contains(sink, "y") {
		t.Fatalf("sink overwritten: %q", sink)
	}
	// 无 sink 的 ctx 调用安全（不 panic、不写入）。
	var plain string
	emitFileDiff(context.Background(), "/a", "x", "y")
	if plain != "" {
		t.Fatal("plain ctx should not write")
	}
}
