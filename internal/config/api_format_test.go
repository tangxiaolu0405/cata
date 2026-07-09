package config

import "testing"

func TestNormalizeAPIFormat(t *testing.T) {
	tests := []struct {
		format, url, label, want string
	}{
		{"openai", "", "", "openai"},
		{"anthropic", "", "", "anthropic"},
		{"", "https://api.anthropic.com/v1/messages", "", "anthropic"},
		{"", "https://api.xiaomimimo.com/anthropic/v1/messages", "mimo", "anthropic"},
		{"", "https://api.deepseek.com/chat/completions", "deepseek", "openai"},
		{"", "", "claude", "anthropic"},
		{"", "https://api.xiaomimimo.com/v1/chat/completions", "mimo", "openai"},
	}
	for _, tc := range tests {
		got := NormalizeAPIFormat(tc.format, tc.url, tc.label)
		if got != tc.want {
			t.Errorf("NormalizeAPIFormat(%q,%q,%q)=%q want %q", tc.format, tc.url, tc.label, got, tc.want)
		}
	}
}

func TestNormalizeLLMConfigBaseURL(t *testing.T) {
	llm := &LLMConfig{
		Provider: "mimo",
		APIURL:   "https://api.xiaomimimo.com/v1",
	}
	normalizeLLMConfig(llm)
	if llm.APIFormat != "openai" {
		t.Fatalf("api_format=%q", llm.APIFormat)
	}
	if llm.APIURL != "https://api.xiaomimimo.com/v1/chat/completions" {
		t.Fatalf("api_url=%q", llm.APIURL)
	}

	llm2 := &LLMConfig{
		Provider:  "mimo",
		APIFormat: "anthropic",
		APIURL:    "https://api.xiaomimimo.com",
	}
	normalizeLLMConfig(llm2)
	if llm2.APIURL != "https://api.xiaomimimo.com/anthropic/v1/messages" {
		t.Fatalf("anthropic api_url=%q", llm2.APIURL)
	}
}
