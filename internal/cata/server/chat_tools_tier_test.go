package server

import (
	"testing"

	"cata/internal/cata/brain"
	"cata/internal/llm"
)

func TestInferToolTierLightPureQuestion(t *testing.T) {
	if got := InferToolTier(1, nil, "这段代码是做什么的？"); got != ToolTierLight {
		t.Fatalf("got %v", got)
	}
}

func TestInferToolTierStandardDefault(t *testing.T) {
	// 无路径/问号，但是真实任务 — 应默认 standard，不能误判 light
	if got := InferToolTier(1, nil, "帮我把 handler 抽出来"); got != ToolTierStandard {
		t.Fatalf("got %v", got)
	}
}

func TestInferToolTierStandardByFileExt(t *testing.T) {
	if got := InferToolTier(1, nil, "请修改 main.go 里的函数"); got != ToolTierStandard {
		t.Fatalf("got %v", got)
	}
}

func TestInferToolTierFullAfterTools(t *testing.T) {
	hist := []llm.Message{{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "1", Type: "function", Function: llm.ToolCallFunction{Name: "read_file"}}}}}
	if got := InferToolTier(2, hist, "继续"); got != ToolTierFull {
		t.Fatalf("got %v", got)
	}
}

func TestPromptProfileForTierNeverMinimalOnMainChat(t *testing.T) {
	for _, tier := range []ToolTier{ToolTierLight, ToolTierStandard, ToolTierFull} {
		if p := PromptProfileForTier(tier); p == brain.PromptProfileMinimal {
			t.Fatalf("tier %v mapped to minimal", tier)
		}
	}
	if PromptProfileForTier(ToolTierLight) != brain.PromptProfileTask {
		t.Fatal("light tools should use task profile")
	}
}

func TestHasStructuralTaskSignals(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"什么是递归", false},
		{"看看 src/foo.go", true},
		{"运行 npm test", true},
		{"```go\nfunc main(){}\n```", true},
		{"打开 https://example.com", true},
	}
	for _, c := range cases {
		if got := hasStructuralTaskSignals(c.text); got != c.want {
			t.Fatalf("text=%q got %v want %v", c.text, got, c.want)
		}
	}
}

func TestIsHighConfidenceReadOnlyQA(t *testing.T) {
	if !isHighConfidenceReadOnlyQA("goroutine 和 thread 有啥区别？") {
		t.Fatal("expected true for short question")
	}
	if isHighConfidenceReadOnlyQA("什么是递归") {
		t.Fatal("no question mark should not be light")
	}
	if isHighConfidenceReadOnlyQA("fix main.go?") {
		t.Fatal("file ext should block light")
	}
}
