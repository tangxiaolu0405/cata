package llm

import "testing"

func TestAppendAPIFormatPath_OpenAI(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"https://api.example.com/v1", "https://api.example.com/v1/chat/completions"},
		{"https://api.xiaomimimo.com/v1/", "https://api.xiaomimimo.com/v1/chat/completions"},
		{"https://api.deepseek.com/chat/completions", "https://api.deepseek.com/chat/completions"},
		{"https://gateway.example.com/proxy/chat/completions/v2", "https://gateway.example.com/proxy/chat/completions/v2"},
	}
	for _, tc := range tests {
		got := AppendAPIFormatPath("openai", tc.in)
		if got != tc.want {
			t.Errorf("openai %q => %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestAppendAPIFormatPath_Anthropic(t *testing.T) {
	base := "https://api.example.com/anthropic"
	want := "https://api.example.com/anthropic/v1/messages"
	if got := AppendAPIFormatPath("anthropic", base); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	full := "https://api.xiaomimimo.com/anthropic/v1/messages"
	if got := AppendAPIFormatPath("anthropic", full); got != full {
		t.Fatalf("full unchanged: got %q", got)
	}
	prefix := "https://gateway.example.com/anthropic/v1/messages/stream"
	if got := AppendAPIFormatPath("anthropic", prefix); got != prefix {
		t.Fatalf("contains path unchanged: got %q", got)
	}
}
