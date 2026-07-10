package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// AnthropicCompatAdapter Anthropic Messages API 兼容协议。
type AnthropicCompatAdapter struct{}

const anthropicAPIVersion = "2023-06-01"

type anthropicWireRequest struct {
	Model       string                 `json:"model"`
	MaxTokens   int                    `json:"max_tokens"`
	System      string                 `json:"system,omitempty"`
	Messages    []anthropicWireMessage `json:"messages"`
	Temperature float64                `json:"temperature,omitempty"`
	Tools       []anthropicWireTool    `json:"tools,omitempty"`
	ToolChoice  interface{}            `json:"tool_choice,omitempty"`
	Stream      bool                   `json:"stream,omitempty"`
}

type anthropicWireMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type anthropicWireTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

func (AnthropicCompatAdapter) Format() string { return APIFormatAnthropic }

func messagesToAnthropicWire(messages []Message) (system string, out []anthropicWireMessage, err error) {
	var systemParts []string
	appendUserBlocks := func(blocks []anthropicContentBlock) {
		if len(blocks) == 0 {
			return
		}
		if len(out) > 0 {
			if last, ok := out[len(out)-1].Content.([]anthropicContentBlock); ok && out[len(out)-1].Role == "user" {
				out[len(out)-1].Content = append(last, blocks...)
				return
			}
		}
		out = append(out, anthropicWireMessage{Role: "user", Content: blocks})
	}
	for _, m := range messages {
		switch m.Role {
		case "system":
			if strings.TrimSpace(m.Content) != "" {
				systemParts = append(systemParts, m.Content)
			}
		case "user":
			if strings.TrimSpace(m.Content) == "" {
				continue
			}
			appendUserBlocks([]anthropicContentBlock{{Type: "text", Text: m.Content}})
		case "assistant":
			blocks, berr := assistantMessageToAnthropicBlocks(m)
			if berr != nil {
				return "", nil, berr
			}
			out = append(out, anthropicWireMessage{Role: "assistant", Content: blocks})
		case "tool":
			appendUserBlocks([]anthropicContentBlock{{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   m.Content,
			}})
		default:
			if strings.TrimSpace(m.Content) != "" {
				appendUserBlocks([]anthropicContentBlock{{Type: "text", Text: m.Content}})
			}
		}
	}
	return strings.Join(systemParts, "\n\n"), out, nil
}

func assistantMessageToAnthropicBlocks(m Message) ([]anthropicContentBlock, error) {
	var blocks []anthropicContentBlock
	if strings.TrimSpace(m.Content) != "" {
		blocks = append(blocks, anthropicContentBlock{Type: "text", Text: m.Content})
	}
	for _, tc := range m.ToolCalls {
		args := strings.TrimSpace(tc.Function.Arguments)
		if args == "" {
			args = "{}"
		}
		if !json.Valid([]byte(args)) {
			return nil, fmt.Errorf("invalid tool arguments for %s", tc.Function.Name)
		}
		blocks = append(blocks, anthropicContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: json.RawMessage(args),
		})
	}
	if len(blocks) == 0 {
		blocks = append(blocks, anthropicContentBlock{Type: "text", Text: ""})
	}
	return blocks, nil
}

func openAIToolsToAnthropic(tools []Tool) []anthropicWireTool {
	out := make([]anthropicWireTool, 0, len(tools))
	for _, t := range tools {
		schema := t.Function.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, anthropicWireTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: schema,
		})
	}
	return out
}

func anthropicToolChoice(toolChoice string) interface{} {
	switch strings.ToLower(strings.TrimSpace(toolChoice)) {
	case "", "auto":
		return map[string]string{"type": "auto"}
	case "none":
		return map[string]string{"type": "any"}
	default:
		return map[string]interface{}{"type": "tool", "name": toolChoice}
	}
}

func marshalAnthropicBody(model string, messages []Message, maxTokens int, temperature float64, tools []Tool, toolChoice string, stream bool) ([]byte, error) {
	system, msgs, err := messagesToAnthropicWire(messages)
	if err != nil {
		return nil, err
	}
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	req := anthropicWireRequest{
		Model:     model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  msgs,
		Stream:    stream,
	}
	if temperature > 0 {
		req.Temperature = temperature
	}
	if len(tools) > 0 {
		req.Tools = openAIToolsToAnthropic(tools)
		req.ToolChoice = anthropicToolChoice(toolChoice)
	}
	return json.Marshal(req)
}

func (p *AnthropicCompatAdapter) BuildRequest(apiURL string, apiKey string, model string, messages []Message, maxTokens int, temperature float64, tools []Tool, toolChoice string, stream bool, disableThinking bool) (*http.Request, error) {
	_ = disableThinking // Anthropic 无 thinking 字段；保留签名与 OpenAI 适配器一致
	reqBody, err := marshalAnthropicBody(model, messages, maxTokens, temperature, tools, toolChoice, stream)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal anthropic request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)
	headerName, headerValue := p.GetAPIKeyHeader(apiKey)
	httpReq.Header.Set(headerName, headerValue)
	return httpReq, nil
}

func parseAnthropicResponseBlocks(content json.RawMessage) (string, []ToolCall, error) {
	if len(content) == 0 {
		return "", nil, nil
	}
	// string content
	var text string
	if err := json.Unmarshal(content, &text); err == nil {
		return strings.TrimSpace(text), nil, nil
	}
	var blocks []anthropicContentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return "", nil, fmt.Errorf("failed to parse anthropic content: %w", err)
	}
	var sb strings.Builder
	var toolCalls []ToolCall
	for _, b := range blocks {
		switch b.Type {
		case "text":
			sb.WriteString(b.Text)
		case "tool_use":
			args := "{}"
			if len(b.Input) > 0 {
				args = string(b.Input)
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   b.ID,
				Type: "function",
				Function: ToolCallFunction{
					Name:      b.Name,
					Arguments: args,
				},
			})
		}
	}
	return strings.TrimSpace(sb.String()), toolCalls, nil
}

func (p *AnthropicCompatAdapter) ParseResponse(body []byte) (string, []ToolCall, error) {
	var resp struct {
		Content []anthropicContentBlock `json:"content"`
		Error   *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", nil, fmt.Errorf("failed to unmarshal anthropic response: %w", err)
	}
	if resp.Error != nil {
		return "", nil, fmt.Errorf("API error: %s (type: %s)", resp.Error.Message, resp.Error.Type)
	}
	raw, err := json.Marshal(resp.Content)
	if err != nil {
		return "", nil, err
	}
	return parseAnthropicResponseBlocks(raw)
}

func (AnthropicCompatAdapter) GetAPIKeyHeader(apiKey string) (string, string) {
	return "x-api-key", apiKey
}
