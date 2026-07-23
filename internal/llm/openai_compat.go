package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"cata/internal/cata/config"
)

// OpenAICompatAdapter OpenAI Chat Completions 兼容协议（DeepSeek / 千问 / Gemini OpenAI 层 / MiMo 等均走此路径）。
type OpenAICompatAdapter struct{}

type wireThinking struct {
	Type string `json:"type"` // enabled | disabled（DeepSeek / MiMo 扩展，非 OpenAI 标准）
}

type wireChatRequest struct {
	Model            string                   `json:"model"`
	Messages         []map[string]interface{} `json:"messages,omitempty"`
	Input            []map[string]interface{} `json:"input,omitempty"` // Responses API
	MaxTokens        int                      `json:"max_tokens,omitempty"`
	MaxOutputTokens  int                      `json:"max_output_tokens,omitempty"` // Responses API
	Temperature      float64                  `json:"temperature,omitempty"`
	Tools            []Tool                   `json:"tools,omitempty"`
	ToolChoice       interface{}              `json:"tool_choice,omitempty"`
	Stream           bool                     `json:"stream,omitempty"`
	Thinking         *wireThinking            `json:"thinking,omitempty"`
}

func (OpenAICompatAdapter) Format() string { return APIFormatOpenAI }

// supportsDeepSeekThinkingWire 是否发送 DeepSeek 风格 thinking / reasoning_content。
// 通用 OpenAI 兼容网关（OpenAI、Gemini openai/、多数代理）不认该字段，乱发可能 400。
func supportsDeepSeekThinkingWire(apiURL string) bool {
	if config.Config != nil {
		p := strings.ToLower(strings.TrimSpace(config.Config.LLM.Provider))
		if strings.Contains(p, "deepseek") || strings.Contains(p, "mimo") {
			return true
		}
	}
	u := strings.ToLower(apiURL)
	return strings.Contains(u, "deepseek") ||
		strings.Contains(u, "xiaomimimo")
}

// resolveWireThinking 非标准 thinking 字段（DeepSeek / MiMo）；由 llm.thinking 与 tools 门控。
// 不支持该扩展的网关一律返回 nil（不写入 JSON）。
func resolveWireThinking(apiURL string, tools []Tool, forceDisabled bool) *wireThinking {
	if !supportsDeepSeekThinkingWire(apiURL) {
		return nil
	}
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

func messagesForChatCompletionsWire(messages []Message, includeReasoningContent bool) []map[string]interface{} {
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
		if includeReasoningContent && m.Role == "assistant" && strings.TrimSpace(m.ReasoningContent) != "" {
			mm["reasoning_content"] = m.ReasoningContent
		}
		out = append(out, mm)
	}
	return out
}

func marshalOpenAIChatBody(apiURL, model string, messages []Message, maxTokens int, temperature float64, tools []Tool, toolChoice string, stream bool, disableThinking bool) ([]byte, error) {
	useThinkingExt := supportsDeepSeekThinkingWire(apiURL)
	responses := isResponsesAPIURL(apiURL)
	req := wireChatRequest{
		Model:       model,
		Temperature: temperature,
		Stream:      stream,
	}
	if responses {
		// xAI / OpenAI Responses：messages→input，max_tokens→max_output_tokens
		req.Input = messagesForChatCompletionsWire(messages, false)
		if maxTokens > 0 {
			req.MaxOutputTokens = maxTokens
		}
	} else {
		req.Messages = messagesForChatCompletionsWire(messages, useThinkingExt)
		req.Thinking = resolveWireThinking(apiURL, tools, disableThinking)
		if maxTokens > 0 {
			req.MaxTokens = maxTokens
		}
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
	reqBody, err := marshalOpenAIChatBody(apiURL, model, messages, maxTokens, temperature, tools, toolChoice, stream, disableThinking)
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
		Output []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		OutputText string `json:"output_text"`
		Error      *struct {
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

	// Chat Completions
	if len(resp.Choices) > 0 {
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

	// Responses API
	if t := strings.TrimSpace(resp.OutputText); t != "" {
		return t, nil, nil
	}
	var b strings.Builder
	for _, item := range resp.Output {
		if item.Type != "" && item.Type != "message" {
			continue
		}
		for _, c := range item.Content {
			if c.Type == "output_text" || c.Type == "text" || c.Type == "" {
				b.WriteString(c.Text)
			}
		}
	}
	if text := strings.TrimSpace(b.String()); text != "" {
		return text, nil, nil
	}
	return "", nil, fmt.Errorf("no response from LLM")
}

func (OpenAICompatAdapter) GetAPIKeyHeader(apiKey string) (string, string) {
	return "Authorization", fmt.Sprintf("Bearer %s", apiKey)
}
