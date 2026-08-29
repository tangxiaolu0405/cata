package llm

import (
	"strings"
	"testing"

	"cata/internal/cata/config"
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
	system, out, err := messagesToAnthropicWire(msgs, ModelCaps{Modalities: map[string]bool{"text": true}})
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

// TestMessagesToAnthropicWireImage 验证 Anthropic user 消息带图时编码为 image block（base64 source）。
func TestMessagesToAnthropicWireImage(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "看图", Media: []MediaRef{{ID: "a.png", MIME: "image/png", Data: "QUJD"}}},
	}
	_, out, err := messagesToAnthropicWire(msgs, ModelCaps{Modalities: map[string]bool{"text": true, "image": true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("out len=%d", len(out))
	}
	blocks, ok := out[0].Content.([]anthropicContentBlock)
	if !ok || len(blocks) != 2 {
		t.Fatalf("blocks=%v", out[0].Content)
	}
	if blocks[0].Type != "text" || blocks[0].Text != "看图" {
		t.Fatalf("text block=%v", blocks[0])
	}
	if blocks[1].Type != "image" || blocks[1].Source == nil ||
		blocks[1].Source.MediaType != "image/png" || blocks[1].Source.Data != "QUJD" {
		t.Fatalf("image block=%v", blocks[1])
	}
}

// TestMessagesToAnthropicWireImageNoCaps 文本模型遇图应报错（不静默丢图）。
func TestMessagesToAnthropicWireImageNoCaps(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "看图", Media: []MediaRef{{ID: "a.png", MIME: "image/png", Data: "QUJD"}}},
	}
	if _, _, err := messagesToAnthropicWire(msgs, ModelCaps{Modalities: map[string]bool{"text": true}}); err == nil {
		t.Fatal("text model with image should error")
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
	deepseekURL := "https://api.deepseek.com/chat/completions"
	if resolveWireThinking(deepseekURL, nil, true) == nil || resolveWireThinking(deepseekURL, nil, true).Type != "disabled" {
		t.Fatal("force disabled on deepseek")
	}
	geminiURL := "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"
	if resolveWireThinking(geminiURL, nil, true) != nil {
		t.Fatal("gemini must not get DeepSeek thinking wire field")
	}
	openaiURL := "https://api.openai.com/v1/chat/completions"
	if resolveWireThinking(openaiURL, nil, false) != nil {
		t.Fatal("openai must not get DeepSeek thinking by default")
	}
}

func TestSupportsDeepSeekThinkingWire(t *testing.T) {
	if !supportsDeepSeekThinkingWire("https://api.deepseek.com/chat/completions") {
		t.Fatal("deepseek url")
	}
	if supportsDeepSeekThinkingWire("https://generativelanguage.googleapis.com/v1beta/openai/chat/completions") {
		t.Fatal("gemini url should not use deepseek thinking")
	}
	// provider=deepseek 但走第三方代理时，不得因标签乱发 thinking
	if config.Config == nil {
		config.Config = &config.AppConfig{}
	}
	prev := config.Config.LLM.Provider
	config.Config.LLM.Provider = "deepseek"
	defer func() { config.Config.LLM.Provider = prev }()
	if supportsDeepSeekThinkingWire("https://agent.chatgpts.top/v1/chat/completions") {
		t.Fatal("third-party proxy must not get DeepSeek thinking wire")
	}
}

func TestOpenAIParseResponse_stringErrorAndSSE(t *testing.T) {
	a := &OpenAICompatAdapter{}
	_, _, err := a.ParseResponse([]byte(`{"error":"quota exceeded"}`))
	if err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("string error: %v", err)
	}
	_, _, err = a.ParseResponse([]byte("data: {\"choices\":[]}\n\n"))
	if err == nil || !strings.Contains(err.Error(), "SSE") {
		t.Fatalf("sse body: %v", err)
	}
}

func TestAppendAPIFormatPath_GeminiOpenAICompat(t *testing.T) {
	base := "https://generativelanguage.googleapis.com/v1beta/openai/"
	want := "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"
	if got := AppendAPIFormatPath("openai", base); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestCandidateAPIURLs(t *testing.T) {
	base := "https://generativelanguage.googleapis.com/v1beta/openai"
	cands := CandidateAPIURLs("openai", base)
	if len(cands) != 2 {
		t.Fatalf("len=%d %v", len(cands), cands)
	}
	if cands[0] != base {
		t.Fatalf("primary %q", cands[0])
	}
	if cands[1] != base+"/chat/completions" {
		t.Fatalf("alt %q", cands[1])
	}
	full := base + "/chat/completions"
	if got := CandidateAPIURLs("openai", full); len(got) != 1 || got[0] != full {
		t.Fatalf("full candidates %v", got)
	}

	respURL := "https://api.x.ai/v1/responses"
	got := CandidateAPIURLs("openai", respURL)
	if len(got) != 2 || got[0] != respURL || got[1] != "https://api.x.ai/v1/chat/completions" {
		t.Fatalf("responses candidates %v", got)
	}
}

func TestMarshalResponsesUsesMaxOutputTokens(t *testing.T) {
	b, err := marshalOpenAIChatBody(
		"https://api.x.ai/v1/responses",
		"grok-3",
		ModelCaps{Modalities: map[string]bool{"text": true}},
		[]Message{{Role: "user", Content: "hi"}},
		128, 0.7, nil, "", false, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, `"max_tokens"`) {
		t.Fatalf("responses body must not use max_tokens: %s", s)
	}
	if !strings.Contains(s, `"max_output_tokens"`) {
		t.Fatalf("expected max_output_tokens: %s", s)
	}
	if !strings.Contains(s, `"input"`) {
		t.Fatalf("expected input: %s", s)
	}
	if strings.Contains(s, `"messages"`) {
		t.Fatalf("responses body must not use messages: %s", s)
	}
}

func TestNormalizeAPIURLNoAppend(t *testing.T) {
	got := NormalizeAPIURL("openai", "https://api.example.com/v1/")
	if got != "https://api.example.com/v1" {
		t.Fatalf("got %q", got)
	}
}
