package config

import "testing"

func TestNormalizeAPIFormat(t *testing.T) {
	tests := []struct {
		format, url, label, want string
	}{
		{"openai", "", "", "openai"},
		{"anthropic", "", "", "anthropic"},
		{"", "https://api.anthropic.com/v1/messages", "", "openai"},
		{"", "", "claude", "anthropic"},
		{"anthropic", "https://api.xiaomimimo.com/v1/chat/completions", "mimo", "anthropic"},
	}
	for _, tc := range tests {
		got := NormalizeAPIFormat(tc.format, tc.url, tc.label)
		if got != tc.want {
			t.Errorf("NormalizeAPIFormat(%q,%q,%q)=%q want %q", tc.format, tc.url, tc.label, got, tc.want)
		}
	}
}

func TestNormalizeLLMConfigKeepsURL(t *testing.T) {
	llm := &LLMConfig{
		APIFormat: "openai",
		APIURL:    "https://api.xiaomimimo.com/v1/",
	}
	normalizeLLMConfig(llm)
	if llm.APIURL != "https://api.xiaomimimo.com/v1" {
		t.Fatalf("openai api_url=%q want trimmed base without forced path", llm.APIURL)
	}

	llm2 := &LLMConfig{
		APIFormat: "anthropic",
		APIURL:    "https://api.xiaomimimo.com/anthropic/",
	}
	normalizeLLMConfig(llm2)
	if llm2.APIURL != "https://api.xiaomimimo.com/anthropic" {
		t.Fatalf("anthropic api_url=%q", llm2.APIURL)
	}
}
