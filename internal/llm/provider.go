package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"cata/internal/config"
)

// Tool 定义给 LLM 暴露的「工具」（兼容 OpenAI tools/function calling）
// 目前仅支持 type=function 的工具。
type Tool struct {
	Type     string       `json:"type"` // 固定为 "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction 描述具体函数
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	// Parameters 是一段 JSON Schema，保持为 RawMessage，调用方自己构造
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ToolCall 表示一次由 LLM 触发的工具调用请求
type ToolCall struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"` // 固定为 "function"
	Function ToolCallFunction  `json:"function"`
}

// ToolCallFunction 是 LLM 返回的具体调用信息
type ToolCallFunction struct {
	Name      string `json:"name"`
	// Arguments 是 JSON 字符串，由上层再反序列化到对应结构
	Arguments string `json:"arguments"`
}

// Provider 定义 LLM 提供商的接口
type Provider interface {
	// BuildRequest 构建 HTTP 请求
	// tools: 允许为空；toolChoice: 目前简单使用字符串（如 "auto"、"none"），留空则走默认策略
	// stream: true 时请求 OpenAI 兼容 SSE（data: 行）
	// disableThinking: true 时强制 DeepSeek thinking=disabled（演进 JSON 等）
	BuildRequest(apiURL string, apiKey string, model string, messages []Message, maxTokens int, temperature float64, tools []Tool, toolChoice string, stream bool, disableThinking bool) (*http.Request, error)
	// ParseResponse 解析 HTTP 响应，返回 assistant 内容和（可选的）tool_calls
	ParseResponse(body []byte) (string, []ToolCall, error)
	// GetAPIKeyHeader 获取 API Key 的 Header 名称和格式
	GetAPIKeyHeader(apiKey string) (string, string)
}

// OpenAIProvider OpenAI 提供商
type OpenAIProvider struct{}

// wireChatRequest 与 OpenAI Chat Completions 对齐的 JSON 负载；messages 单独构造以满足各兼容网关对 content 的校验。
type wireThinking struct {
	Type string `json:"type"` // enabled | disabled
}

type wireChatRequest struct {
	Model         string                   `json:"model"`
	Messages      []map[string]interface{} `json:"messages"`
	MaxTokens     int                      `json:"max_tokens,omitempty"`
	Temperature   float64                  `json:"temperature,omitempty"`
	Tools         []Tool                   `json:"tools,omitempty"`
	ToolChoice    interface{}              `json:"tool_choice,omitempty"`
	Stream        bool                     `json:"stream,omitempty"`
	Thinking      *wireThinking            `json:"thinking,omitempty"`
}

func isDeepSeekAPIURL(apiURL string) bool {
	return strings.Contains(strings.ToLower(apiURL), "deepseek.com")
}

// resolveDeepSeekThinking 见 https://api-docs.deepseek.com/guides/thinking_mode#tool-calls
func resolveDeepSeekThinking(apiURL string, tools []Tool, forceDisabled bool) *wireThinking {
	if !isDeepSeekAPIURL(apiURL) {
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

// messagesForChatCompletionsWire 将 Message 转为 JSON 对象切片。
// 对「仅 tool_calls、无 assistant 正文」的消息显式写入 content:null，避免部分兼容实现（如 DashScope）在省略 content 时误判类型并报 400。
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

func marshalChatCompletionsBody(apiURL, model string, messages []Message, maxTokens int, temperature float64, tools []Tool, toolChoice string, stream bool, disableThinking bool) ([]byte, error) {
	req := wireChatRequest{
		Model:       model,
		Messages:    messagesForChatCompletionsWire(messages),
		MaxTokens:   maxTokens,
		Temperature: temperature,
		Stream:      stream,
		Thinking:    resolveDeepSeekThinking(apiURL, tools, disableThinking),
	}
	if len(tools) > 0 {
		req.Tools = tools
		if toolChoice != "" {
			req.ToolChoice = toolChoice
		}
	}
	return json.Marshal(req)
}

func (p *OpenAIProvider) BuildRequest(apiURL string, apiKey string, model string, messages []Message, maxTokens int, temperature float64, tools []Tool, toolChoice string, stream bool, disableThinking bool) (*http.Request, error) {
	reqBody, err := marshalChatCompletionsBody(apiURL, model, messages, maxTokens, temperature, tools, toolChoice, stream, disableThinking)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	headerName, headerValue := p.GetAPIKeyHeader(apiKey)
	httpReq.Header.Set(headerName, headerValue)

	return httpReq, nil
}

func (p *OpenAIProvider) ParseResponse(body []byte) (string, []ToolCall, error) {
	// 只解析我们关心的字段，兼容 OpenAI 的 tools / tool_calls
	var resp struct {
		Choices []struct {
			Index     int     `json:"index"`
			Message   Message `json:"message"`
			ToolCalls []ToolCall `json:"tool_calls,omitempty"`
			FinishReason string  `json:"finish_reason"`
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

func (p *OpenAIProvider) GetAPIKeyHeader(apiKey string) (string, string) {
	return "Authorization", fmt.Sprintf("Bearer %s", apiKey)
}

// QwenProvider 通义千问提供商（DashScope API）
// 注意：DashScope 支持 OpenAI 兼容格式，但响应格式略有不同
type QwenProvider struct{}

// QwenResponse 千问 API 响应格式（DashScope）
type QwenResponse struct {
	Output struct {
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	} `json:"output"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
	RequestID string `json:"request_id"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
}

func (p *QwenProvider) BuildRequest(apiURL string, apiKey string, model string, messages []Message, maxTokens int, temperature float64, tools []Tool, toolChoice string, stream bool, disableThinking bool) (*http.Request, error) {
	reqBody, err := marshalChatCompletionsBody(apiURL, model, messages, maxTokens, temperature, tools, toolChoice, stream, disableThinking)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	
	// 检查 API key
	if apiKey == "" {
		return nil, fmt.Errorf("API key is empty (provider: qwen)")
	}
	
	headerName, headerValue := p.GetAPIKeyHeader(apiKey)
	httpReq.Header.Set(headerName, headerValue)

	// 始终记录关键信息（用于排查 EOF 问题）
	log.Printf("QwenProvider Request: URL=%s, Model=%s, APIKey present=%v, Header=%s: %s...", 
		apiURL, model, apiKey != "", headerName, 
		func() string {
			if len(headerValue) > 30 {
				return headerValue[:30]
			}
			return headerValue
		}())
	
	// 检查 URL 区域匹配提示
	if strings.Contains(apiURL, "dashscope.aliyuncs.com") && !strings.Contains(apiURL, "dashscope-intl") && !strings.Contains(apiURL, "dashscope-us") {
		log.Printf("INFO: Using China region endpoint. If you're outside China and getting EOF errors, try: https://dashscope-intl.aliyuncs.com/compatible-mode/v1/chat/completions")
	}

	// 调试日志
	if os.Getenv("DEBUG_LLM") == "true" {
		log.Printf("DEBUG QwenProvider: Request Body: %s", string(reqBody))
		log.Printf("DEBUG QwenProvider: All Headers: %v", httpReq.Header)
		log.Printf("DEBUG QwenProvider: Request Method: %s", httpReq.Method)
		log.Printf("DEBUG QwenProvider: Request URL: %s", httpReq.URL.String())
	}

	return httpReq, nil
}

func (p *QwenProvider) ParseResponse(body []byte) (string, []ToolCall, error) {
	// DashScope OpenAI 兼容模式返回的是 OpenAI 格式的响应
	// 先尝试解析为 OpenAI 格式
	var openAIResp struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Index        int       `json:"index"`
			Message      Message   `json:"message"`
			ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
			FinishReason string    `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error,omitempty"`
	}
	
	if err := json.Unmarshal(body, &openAIResp); err != nil {
		// 如果解析失败，尝试旧的 DashScope 格式（向后兼容）
		var qwenResp QwenResponse
		if err2 := json.Unmarshal(body, &qwenResp); err2 != nil {
			return "", nil, fmt.Errorf("failed to unmarshal response (both OpenAI and DashScope formats): %w (OpenAI), %v (DashScope)", err, err2)
		}
		
		// 检查错误
		if qwenResp.Code != "" && qwenResp.Code != "200" {
			return "", nil, fmt.Errorf("API error: %s (code: %s)", qwenResp.Message, qwenResp.Code)
		}
		
		if len(qwenResp.Output.Choices) == 0 {
			return "", nil, fmt.Errorf("no response from LLM")
		}
		
		return qwenResp.Output.Choices[0].Message.Content, nil, nil
	}
	
	// OpenAI 兼容格式解析成功
	if openAIResp.Error != nil {
		return "", nil, fmt.Errorf("API error: %s (type: %s, code: %s)", 
			openAIResp.Error.Message, openAIResp.Error.Type, openAIResp.Error.Code)
	}
	
	if len(openAIResp.Choices) == 0 {
		return "", nil, fmt.Errorf("no response from LLM")
	}
	
	first := openAIResp.Choices[0]
	text := strings.TrimSpace(first.Message.Content)
	if text == "" {
		text = strings.TrimSpace(first.Message.ReasoningContent)
	}
	return text, first.Message.ToolCalls, nil
}

func (p *QwenProvider) GetAPIKeyHeader(apiKey string) (string, string) {
	// 千问使用 Authorization: Bearer <token> 格式
	return "Authorization", fmt.Sprintf("Bearer %s", apiKey)
}

// GetProvider 根据提供商名称获取 Provider 实例
// 支持的提供商：
//   - openai / deepseek: OpenAI 兼容 Chat Completions（DeepSeek 见 https://api.deepseek.com）
//   - qwen/tongyi/dashscope: 通义千问（DashScope API）
func GetProvider(providerName string) Provider {
	switch strings.ToLower(providerName) {
	case "qwen", "tongyi", "dashscope":
		return &OpenAIProvider{}
	case "deepseek":
		return &OpenAIProvider{}
	case "openai", "":
		fallthrough
	default:
		return &OpenAIProvider{}
	}
}

// RegisterProvider 注册自定义 Provider（用于插件或扩展）
// 注意：当前版本暂不支持运行时注册，需要在代码中添加
// 未来版本可能会支持通过配置文件或插件注册自定义 Provider
var customProviders = make(map[string]Provider)

// RegisterCustomProvider 注册自定义 Provider（实验性功能）
func RegisterCustomProvider(name string, provider Provider) {
	customProviders[strings.ToLower(name)] = provider
}

// GetProviderWithCustom 获取 Provider，包括自定义注册的
func GetProviderWithCustom(providerName string) Provider {
	name := strings.ToLower(providerName)
	if custom, ok := customProviders[name]; ok {
		return custom
	}
	return GetProvider(providerName)
}
