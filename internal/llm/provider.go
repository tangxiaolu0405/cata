package llm

import (
	"encoding/json"
	"net/http"
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

// APIAdapter HTTP 协议适配器（由 llm.api_format 选择，与 provider 标签无关）。
type APIAdapter interface {
	Format() string
	// BuildRequest 构建 HTTP 请求；disableThinking 仅 OpenAI 兼容网关的 thinking 扩展字段有效。
	BuildRequest(apiURL string, apiKey string, model string, messages []Message, maxTokens int, temperature float64, tools []Tool, toolChoice string, stream bool, disableThinking bool) (*http.Request, error)
	ParseResponse(body []byte) (string, []ToolCall, error)
	GetAPIKeyHeader(apiKey string) (string, string)
}

// Provider 保留别名，避免大范围重命名。
type Provider = APIAdapter

var (
	openAIAdapter    APIAdapter = &OpenAICompatAdapter{}
	anthropicAdapter APIAdapter = &AnthropicCompatAdapter{}
	customAdapters   = make(map[string]APIAdapter)
)

// GetAPIAdapter 按 api_format 返回协议适配器（兜底方案）。
func GetAPIAdapter(apiFormat string) APIAdapter {
	if custom, ok := customAdapters[ResolveAPIFormat(apiFormat, "", "")]; ok {
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
	customAdapters[ResolveAPIFormat(apiFormat, "", "")] = adapter
}

// GetProvider 兼容旧调用；现按 api_format 解析。
func GetProvider(apiFormat string) Provider {
	return GetAPIAdapter(apiFormat)
}

// RegisterCustomProvider 兼容旧名。
func RegisterCustomProvider(apiFormat string, provider Provider) {
	RegisterCustomAdapter(apiFormat, provider)
}

// GetProviderWithCustom 兼容旧名。
func GetProviderWithCustom(apiFormat string) Provider {
	return GetAPIAdapter(apiFormat)
}
