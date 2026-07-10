package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"cata/internal/config"
)

// OpenAICompatAdapter OpenAI Chat Completions 兼容协议（DeepSeek / 千问 / MiMo 等均走此路径）。
type OpenAICompatAdapter struct{}

type wireThinking struct {
	Type string `json:"type"` // enabled | disabled
}

type wireChatRequest struct {
	Model       string                   `json:"model"`
	Messages    []map[string]interface{} `json:"messages"`
	MaxTokens   int                      `json:"max_tokens,omitempty"`
	Temperature float64                  `json:"temperature,omitempty"`
	Tools       []Tool                   `json:"tools,omitempty"`
	ToolChoice  interface{}              `json:"tool_choice,omitempty"`
	Stream      bool                     `json:"stream,omitempty"`
	Thinking    *wireThinking            `json:"thinking,omitempty"`
}

func (OpenAICompatAdapter) Format() string { return APIFormatOpenAI }

// resolveWireThinking 非标准 thinking 字段（DeepSeek / MiMo 等）；由 llm.thinking 与 tools 门控。
func resolveWireThinking(tools []Tool, forceDisabled bool) *wireThinking {
	if forceDisabled {
		return &wireThinking{Type: "disabled"}
	}
	mode := "auto"
	if config.Config != nil {
		switch strings.ToLower(strings.TrimSpace(config.Config.LLM.Thinking)) {
		case "enabled", "disabled":
			mode = strings.ToLower(strings.TrimSpace(config.Config.LLM.Thinking))
		}
	}
	switch mode {
	case "disabled":
		return &wireThinking{Type: "disabled"}
	case "enabled":
		return &wireThinking{Type: "enabled"}
	default:
		if len(tools) > 0 {
			return &wireThinking{Type: "disabled"}
		}
		return nil
	}
}

func messagesForChatCompletionsWire(messages []Message) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(messages))
	for _, m := range messages {
		mm := map[string]interface{}{"role": m.Role}
		onlyToolCalls := m.Role == "assistant" && len(m.ToolCalls) > 0 && strings.TrimSpace(m.Content) == ""
		if onlyToolCalls {
			mm["content"] = nil
		} else {
			mm["content"] = m.Content
		}
		if len(m.ToolCalls) > 0 {
			mm["tool_calls"] = m.ToolCalls
		}
		if m.ToolCallID != "" {
			mm["tool_call_id"] = m.ToolCallID
		}
		if m.Name != "" {
			mm["name"] = m.Name
		}
		if m.Role == "assistant" && strings.TrimSpace(m.ReasoningContent) != "" {
			mm["reasoning_content"] = m.ReasoningContent
		}
		out = append(out, mm)
	}
	return out
}

func marshalOpenAIChatBody(model string, messages []Message, maxTokens int, temperature float64, tools []Tool, toolChoice string, stream bool, disableThinking bool) ([]byte, error) {
	req := wireChatRequest{
		Model:       model,
		Messages:    messagesForChatCompletionsWire(messages),
		Temperature: temperature,
		Stream:      stream,
		Thinking:    resolveWireThinking(tools, disableThinking),
	}
	if maxTokens > 0 {
		req.MaxTokens = maxTokens
	}
	if len(tools) > 0 {
		req.Tools = tools
		if toolChoice != "" {
			req.ToolChoice = toolChoice
		}
	}
	return json.Marshal(req)
}

func (p *OpenAICompatAdapter) BuildRequest(apiURL string, apiKey string, model string, messages []Message, maxTokens int, temperature float64, tools []Tool, toolChoice string, stream bool, disableThinking bool) (*http.Request, error) {
	reqBody, err := marshalOpenAIChatBody(model, messages, maxTokens, temperature, tools, toolChoice, stream, disableThinking)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	headerName, headerValue := p.GetAPIKeyHeader(apiKey)
	httpReq.Header.Set(headerName, headerValue)
	return httpReq, nil
}

func (p *OpenAICompatAdapter) ParseResponse(body []byte) (string, []ToolCall, error) {
	var resp struct {
		Choices []struct {
			Message      Message    `json:"message"`
			ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
			FinishReason string     `json:"finish_reason"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error,omitempty"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return "", nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	if resp.Error != nil {
		return "", nil, fmt.Errorf("API error: %s (type: %s, code: %s)",
			resp.Error.Message, resp.Error.Type, resp.Error.Code)
	}
	if len(resp.Choices) == 0 {
		return "", nil, fmt.Errorf("no response from LLM")
	}

	first := resp.Choices[0]
	tc := first.Message.ToolCalls
	if len(tc) == 0 {
		tc = first.ToolCalls
	}
	text := strings.TrimSpace(first.Message.Content)
	if text == "" {
		text = strings.TrimSpace(first.Message.ReasoningContent)
	}
	return text, tc, nil
}

func (OpenAICompatAdapter) GetAPIKeyHeader(apiKey string) (string, string) {
	return "Authorization", fmt.Sprintf("Bearer %s", apiKey)
}
