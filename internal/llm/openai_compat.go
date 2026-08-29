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
	Model           string                   `json:"model"`
	Messages        []map[string]interface{} `json:"messages,omitempty"`
	Input           []map[string]interface{} `json:"input,omitempty"` // Responses API
	MaxTokens       int                      `json:"max_tokens,omitempty"`
	MaxOutputTokens int                      `json:"max_output_tokens,omitempty"` // Responses API
	Temperature     float64                  `json:"temperature,omitempty"`
	Tools           []Tool                   `json:"tools,omitempty"`
	ToolChoice      interface{}              `json:"tool_choice,omitempty"`
	Stream          bool                     `json:"stream,omitempty"`
	Thinking        *wireThinking            `json:"thinking,omitempty"`
}

func (OpenAICompatAdapter) Format() string { return APIFormatOpenAI }

// supportsDeepSeekThinkingWire 是否发送 DeepSeek 风格 thinking / reasoning_content。
// 通用 OpenAI 兼容网关（OpenAI、Gemini openai/、多数第三方代理）不认该字段，乱发可能 400 或返回空 JSON。
// 仅按 api_url 判断；勿因 provider 标签写成 deepseek、但实际走代理就发扩展字段。
func supportsDeepSeekThinkingWire(apiURL string) bool {
	u := strings.ToLower(apiURL)
	return strings.Contains(u, "deepseek.com") ||
		strings.Contains(u, "api.deepseek") ||
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
	return messagesForChatCompletionsWireWithCaps(messages, includeReasoningContent, ModelCaps{Modalities: map[string]bool{"text": true}})
}

// messagesForChatCompletionsWireWithCaps 同前，但按模型能力编码带图消息（content[]）。
// 调用方（BuildRequest 前）应先用 capsForModel 解析目标模型能力；带图文本模型会在此被拒绝。
func messagesForChatCompletionsWireWithCaps(messages []Message, includeReasoningContent bool, caps ModelCaps) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(messages))
	for _, m := range messages {
		mm := map[string]interface{}{"role": m.Role}
		onlyToolCalls := m.Role == "assistant" && len(m.ToolCalls) > 0 && strings.TrimSpace(m.Content) == ""
		if onlyToolCalls {
			mm["content"] = nil
		} else {
			content, err := encodeContentForWire(caps, m)
			if err != nil {
				// 带图消息但模型不支持 image：不静默丢图，交给调用层报错。
				// 此处用占位文本，确保请求 JSON 仍合法；更早的能力路由应已拦截。
				mm["content"] = "[image attachment not supported by current model]"
			} else {
				mm["content"] = content
			}
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

func marshalOpenAIChatBody(apiURL, model string, caps ModelCaps, messages []Message, maxTokens int, temperature float64, tools []Tool, toolChoice string, stream bool, disableThinking bool) ([]byte, error) {
	useThinkingExt := supportsDeepSeekThinkingWire(apiURL)
	responses := isResponsesAPIURL(apiURL)
	req := wireChatRequest{
		Model:       model,
		Temperature: temperature,
		Stream:      stream,
	}
	if responses {
		// xAI / OpenAI Responses：messages→input，max_tokens→max_output_tokens
		req.Input = messagesForChatCompletionsWireWithCaps(messages, false, caps)
		if maxTokens > 0 {
			req.MaxOutputTokens = maxTokens
		}
	} else {
		req.Messages = messagesForChatCompletionsWireWithCaps(messages, useThinkingExt, caps)
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

func (p *OpenAICompatAdapter) BuildRequest(apiURL string, apiKey string, model string, caps ModelCaps, messages []Message, maxTokens int, temperature float64, tools []Tool, toolChoice string, stream bool, disableThinking bool) (*http.Request, error) {
	reqBody, err := marshalOpenAIChatBody(apiURL, model, caps, messages, maxTokens, temperature, tools, toolChoice, stream, disableThinking)
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
	trim := bytes.TrimSpace(body)
	if len(trim) == 0 {
		return "", nil, fmt.Errorf("no response from LLM (empty body)")
	}
	// 部分代理把 SSE 标成 application/json；交给调用方用流式解析。
	if looksLikeSSEChatBody(trim) {
		return "", nil, errBodyLooksLikeSSE
	}

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
		OutputText string          `json:"output_text"`
		Error      json.RawMessage `json:"error,omitempty"`
	}

	if err := json.Unmarshal(trim, &resp); err != nil {
		return "", nil, fmt.Errorf("failed to unmarshal response: %w; body=%s", err, truncateForErr(trim, 240))
	}
	if len(resp.Error) > 0 && string(resp.Error) != "null" {
		if msg := formatOpenAIErrorField(resp.Error); msg != "" {
			return "", nil, fmt.Errorf("API error: %s", msg)
		}
	}
	// 部分网关：{"object":"error","message":"...","type":"BadRequest","code":400}
	var altErr struct {
		Object  string `json:"object"`
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	}
	if json.Unmarshal(trim, &altErr) == nil && strings.EqualFold(altErr.Object, "error") && strings.TrimSpace(altErr.Message) != "" {
		return "", nil, fmt.Errorf("API error: %s (type: %s)", altErr.Message, altErr.Type)
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
	return "", nil, fmt.Errorf("no response from LLM; body=%s", truncateForErr(trim, 280))
}

var errBodyLooksLikeSSE = fmt.Errorf("body looks like SSE stream")

func looksLikeSSEChatBody(body []byte) bool {
	s := string(bytes.TrimSpace(body))
	if strings.HasPrefix(s, "data:") {
		return true
	}
	// 常见：先空行或注释再 data:
	return strings.Contains(s, "\ndata:") || strings.HasPrefix(s, ":") && strings.Contains(s, "data:")
}

func formatOpenAIErrorField(raw json.RawMessage) string {
	var asStr string
	if err := json.Unmarshal(raw, &asStr); err == nil {
		return strings.TrimSpace(asStr)
	}
	var obj struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		parts := []string{}
		if obj.Message != "" {
			parts = append(parts, obj.Message)
		}
		if obj.Type != "" {
			parts = append(parts, "type="+obj.Type)
		}
		if obj.Code != "" {
			parts = append(parts, "code="+obj.Code)
		}
		return strings.Join(parts, " ")
	}
	return truncateForErr(raw, 200)
}

func truncateForErr(b []byte, max int) string {
	s := strings.TrimSpace(string(b))
	s = strings.ReplaceAll(s, "\n", "\\n")
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func (OpenAICompatAdapter) GetAPIKeyHeader(apiKey string) (string, string) {
	return "Authorization", fmt.Sprintf("Bearer %s", apiKey)
}
