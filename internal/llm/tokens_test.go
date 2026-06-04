package llm

import "testing"

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
