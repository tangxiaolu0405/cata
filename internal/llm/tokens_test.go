package llm

import (
	"testing"
)

func TestDefaultContextWindow(t *testing.T) {
	cases := map[string]int{
		"deepseek-v4-flash": contextWindow1M,
		"deepseek-v4-pro":   contextWindow1M,
		"gpt-4o":            contextWindow1M,
		"gpt-4.1":           contextWindow1M,
		"gpt-3.5-turbo":     16385,
		"claude-3-5-sonnet": contextWindow200K,
		"qwen-max":          contextWindow1M,
		"qwen-turbo":        32000,
		"unknown-model":     contextWindow1M,
	}
	for model, want := range cases {
		if got := DefaultContextWindow(model); got != want {
			t.Fatalf("%q: got %d want %d", model, got, want)
		}
	}
}

// TestEstimateMessagesTokensMedia 图片附件按 image_token_estimate 计入估算：
// 带图消息明显高于同正文无图消息，且每张图贡献固定额度（压缩预算/超窗判定依赖）。
func TestEstimateMessagesTokensMedia(t *testing.T) {
	// 零图基线。
	base := []Message{{Role: "user", Content: "看图"}}
	one := []Message{{Role: "user", Content: "看图", Media: []MediaRef{{ID: "a.png", MIME: "image/png", Data: "QUJD"}}}}
	two := []Message{{Role: "user", Content: "看图", Media: []MediaRef{{ID: "a.png", MIME: "image/png", Data: "QUJD"}, {ID: "b.png", MIME: "image/png", Data: "QUJD"}}}}

	estBase := estimateMessagesTokens(base)
	estOne := estimateMessagesTokens(one)
	estTwo := estimateMessagesTokens(two)
	if estOne <= estBase {
		t.Fatalf("media est %d should exceed text-only base %d", estOne, estBase)
	}
	// 两张图 ≈ 单图 + 1 张图额度（默认 1000）。
	if got := estTwo - estOne; got != imageTokenEstimate() {
		t.Fatalf("per-image delta=%d want %d", got, imageTokenEstimate())
	}
}
