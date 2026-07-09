package llm

import (
	"strings"
	"testing"
)

func TestResolveAPIFormat(t *testing.T) {
	got := ResolveAPIFormat("anthropic", "https://api.anthropic.com/v1/messages", "custom")
	if got != APIFormatAnthropic {
		t.Fatalf("got %q", got)
	}
	got = ResolveAPIFormat("", "https://api.xiaomimimo.com/v1/chat/completions", "mimo")
	if got != APIFormatOpenAI {
		t.Fatalf("got %q", got)
	}
}

func TestGetAPIAdapter(t *testing.T) {
	if GetAPIAdapter("openai").Format() != APIFormatOpenAI {
		t.Fatal("openai adapter")
	}
	if GetAPIAdapter("anthropic").Format() != APIFormatAnthropic {
		t.Fatal("anthropic adapter")
	}
}

func TestMessagesToAnthropicWire(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello", ToolCalls: []ToolCall{{
			ID: "tc1", Type: "function",
			Function: ToolCallFunction{Name: "read_file", Arguments: `{"path":"a.txt"}`},
		}}},
		{Role: "tool", ToolCallID: "tc1", Content: "file body"},
	}
	system, out, err := messagesToAnthropicWire(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(system, "sys") {
		t.Fatalf("system=%q", system)
	}
	if len(out) != 3 {
		t.Fatalf("messages len=%d", len(out))
	}
	if out[1].Role != "assistant" {
		t.Fatalf("assistant role=%s", out[1].Role)
	}
}

func TestAnthropicParseResponse(t *testing.T) {
	body := []byte(`{
		"content": [
			{"type":"text","text":"done"},
			{"type":"tool_use","id":"tu1","name":"list_files","input":{"path":"."}}
		]
	}`)
	text, tc, err := anthropicAdapter.ParseResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if text != "done" {
		t.Fatalf("text=%q", text)
	}
	if len(tc) != 1 || tc[0].Function.Name != "list_files" {
		t.Fatalf("tool_calls=%+v", tc)
	}
}

func TestResolveWireThinking(t *testing.T) {
	if resolveWireThinking(nil, true) == nil || resolveWireThinking(nil, true).Type != "disabled" {
		t.Fatal("force disabled")
	}
}
