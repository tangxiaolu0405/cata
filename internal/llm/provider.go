package llm

import (
	"encoding/json"
	"net/http"
	"sync"
)

// Tool 定义给 LLM 暴露的「工具」（兼容 OpenAI tools/function calling）。
type Tool struct {
	Type     string       `json:"type"` // 固定为 "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction 描述具体函数。
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ToolCall 表示一次由 LLM 触发的工具调用请求。
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // 固定为 "function"
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction 是 LLM 返回的具体调用信息。
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// UnmarshalJSON 兼容 arguments 为 JSON string 或 object/array（非流式 DeepSeek 等）。
func (f *ToolCallFunction) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	f.Name = raw.Name
	if len(raw.Arguments) == 0 || string(raw.Arguments) == "null" {
		f.Arguments = ""
		return nil
	}
	if raw.Arguments[0] == '"' {
		var s string
		if err := json.Unmarshal(raw.Arguments, &s); err != nil {
			return err
		}
		f.Arguments = s
		return nil
	}
	f.Arguments = string(raw.Arguments)
	return nil
}

// APIAdapter HTTP 协议适配器（由 llm.api_format 选择，与 provider 标签无关）。
type APIAdapter interface {
	Format() string
	// BuildRequest 构建 HTTP 请求；disableThinking 仅 OpenAI 兼容网关的 thinking 扩展字段有效。
	// caps 为目标模型的多模态能力（带图消息据此编码为 content[]）。
	BuildRequest(apiURL string, apiKey string, model string, caps ModelCaps, messages []Message, maxTokens int, temperature float64, tools []Tool, toolChoice string, stream bool, disableThinking bool) (*http.Request, error)
	ParseResponse(body []byte) (string, []ToolCall, error)
	GetAPIKeyHeader(apiKey string) (string, string)
}

var (
	openAIAdapter    APIAdapter = &OpenAICompatAdapter{}
	anthropicAdapter APIAdapter = &AnthropicCompatAdapter{}
	customAdaptersMu sync.RWMutex
	customAdapters   = make(map[string]APIAdapter)
)

// GetAPIAdapter 按 api_format 返回协议适配器（兜底方案）。
func GetAPIAdapter(apiFormat string) APIAdapter {
	customAdaptersMu.RLock()
	custom, ok := customAdapters[ResolveAPIFormat(apiFormat, "", "")]
	customAdaptersMu.RUnlock()
	if ok {
		return custom
	}
	switch ResolveAPIFormat(apiFormat, "", "") {
	case APIFormatAnthropic:
		return anthropicAdapter
	default:
		return openAIAdapter
	}
}

// RegisterCustomAdapter 注册自定义协议适配器（实验性，key 为 api_format）。
func RegisterCustomAdapter(apiFormat string, adapter APIAdapter) {
	if adapter == nil {
		return
	}
	customAdaptersMu.Lock()
	customAdapters[ResolveAPIFormat(apiFormat, "", "")] = adapter
	customAdaptersMu.Unlock()
}
