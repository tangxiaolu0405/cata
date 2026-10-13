package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cata/internal/cata/brain"
	"cata/internal/llm"
)

// mkHistory 造 n 轮 user/assistant/tool 历史，tool 正文足够大以触发溢出。
// 每轮 ToolCallID 唯一，避免去重键碰撞。
func mkHistory(rounds int, toolBody string) []llm.Message {
	var h []llm.Message
	for i := 0; i < rounds; i++ {
		id := "call-" + string(rune('a'+i))
		h = append(h,
			llm.Message{Role: "user", Content: "q"},
			llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: id, Function: llm.ToolCallFunction{Name: "run_command"}}}},
			llm.Message{Role: "tool", ToolCallID: id, Name: "run_command", Content: toolBody},
		)
	}
	return h
}

func TestCompactHistory_KeepsRecentRounds(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("x", spillMinBytes+100)
	h := mkHistory(spillKeepRecentRounds+3, body)
	st := newSpillState()

	n := compactHistoryForContext(h, dir, "sess1", st)
	if n == 0 {
		t.Fatal("expected some tool messages compacted")
	}

	// 最近 spillKeepRecentRounds 轮必须保持全文。
	cut := recentRoundsStart(h, spillKeepRecentRounds)
	for i := cut; i < len(h); i++ {
		if h[i].Role == "tool" && h[i].Content != body {
			t.Fatalf("recent tool message at %d was compacted: %q", i, h[i].Content)
		}
	}
	// 早期 tool 必须已摘要化。
	spilled := 0
	for i := 0; i < cut; i++ {
		if h[i].Role == "tool" {
			if !isAlreadySpilled(h[i].Content) {
				t.Fatalf("early tool at %d not compacted: %q", i, h[i].Content)
			}
			spilled++
		}
	}
	if spilled == 0 {
		t.Fatal("no early tool message spilled")
	}

	// 摘要里必须带可 read_file 的相对产出区路径，且文件真实存在。
	for i := 0; i < cut; i++ {
		if h[i].Role != "tool" {
			continue
		}
		rel := extractSpillPath(h[i].Content)
		if rel == "" {
			t.Fatalf("summary missing spill path: %q", h[i].Content)
		}
		if !strings.HasPrefix(rel, brain.SessionSpillRelPrefix) {
			t.Fatalf("spill path not under %s: %q", brain.SessionSpillRelPrefix, rel)
		}
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if _, err := os.Stat(abs); err != nil {
			t.Fatalf("spill file missing at %s: %v", abs, err)
		}
	}
}

func TestCompactHistory_Idempotent(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("y", spillMinBytes+100)
	h := mkHistory(spillKeepRecentRounds+2, body)
	st := newSpillState()

	first := compactHistoryForContext(h, dir, "sess2", st)
	if first == 0 {
		t.Fatal("first pass should compact")
	}
	snapshot := make([]string, len(h))
	for i := range h {
		snapshot[i] = h[i].Content
	}
	// 再跑一次：不应再有任何改动，且正文与第一次一致。
	second := compactHistoryForContext(h, dir, "sess2", st)
	if second != 0 {
		t.Fatalf("second pass should be no-op, got %d", second)
	}
	for i := range h {
		if h[i].Content != snapshot[i] {
			t.Fatalf("message %d changed on second pass", i)
		}
	}
}

func TestCompactHistory_SkipsAssistantToolCalls(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("z", spillAssistantMinBytes+100)
	h := []llm.Message{
		{Role: "user", Content: "q"},
		{Role: "assistant", Content: big, ToolCalls: []llm.ToolCall{{ID: "c", Function: llm.ToolCallFunction{Name: "run_command"}}}},
		{Role: "tool", ToolCallID: "c", Name: "run_command", Content: "short"},
		// 补足轮数，让第一条 assistant 落在「早期」区间
		{Role: "user", Content: "q2"}, {Role: "assistant", Content: "a"},
		{Role: "user", Content: "q3"}, {Role: "assistant", Content: "a"},
		{Role: "user", Content: "q4"}, {Role: "assistant", Content: "a"},
		{Role: "user", Content: "q5"}, {Role: "assistant", Content: "a"},
		{Role: "user", Content: "q6"}, {Role: "assistant", Content: "a"},
	}
	st := newSpillState()
	compactHistoryForContext(h, dir, "sess3", st)
	if h[1].Content != big {
		t.Fatalf("assistant with tool_calls must not be compacted: %q", h[1].Content)
	}
}

func TestClearSessionSpill(t *testing.T) {
	dir := t.TempDir()
	rel, err := brain.WriteSessionSpill(dir, "sessX", "001-tool.txt", "data")
	if err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("spill not written: %v", err)
	}
	if err := brain.ClearSessionSpill(dir, "sessX"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(abs)); !os.IsNotExist(err) {
		t.Fatalf("spill dir should be removed, stat err=%v", err)
	}
	// 幂等：再清一次不报错。
	if err := brain.ClearSessionSpill(dir, "sessX"); err != nil {
		t.Fatalf("second clear should be no-op: %v", err)
	}
}

// extractSpillPath 从摘要正文里取出反引号包裹的相对路径。
func extractSpillPath(summary string) string {
	i := strings.Index(summary, "`")
	if i < 0 {
		return ""
	}
	rest := summary[i+1:]
	j := strings.Index(rest, "`")
	if j < 0 {
		return ""
	}
	return rest[:j]
}
