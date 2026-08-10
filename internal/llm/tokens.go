package llm

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"cata/internal/cata/config"
)

// 粗算 token：混合中英文约 3.2 字符/token（无 tiktoken 时的保守估计）。
const charsPerTokenEstimate = 3.2

// 主流 OpenAI 兼容 / DeepSeek 等模型上下文已普遍为 1M；Claude 等仍单独标注。
const contextWindow1M = 1_000_000
const contextWindow200K = 200_000

// DefaultContextWindow 按模型名猜测上下文窗口；未知模型默认 1M。
func DefaultContextWindow(model string) int {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(m, "gpt-3.5"):
		return 16385
	case strings.Contains(m, "claude"):
		return contextWindow200K
	case strings.Contains(m, "qwen"), strings.Contains(m, "tongyi"), strings.Contains(m, "dashscope"):
		if strings.Contains(m, "turbo") && !strings.Contains(m, "max") &&
			!strings.Contains(m, "plus") && !strings.Contains(m, "long") {
			return 32000
		}
		return contextWindow1M
	default:
		// deepseek-v4*、gpt-4*、o1/o3、gemini 等
		return contextWindow1M
	}
}

// ContextWindowTokens 返回当前客户端使用的上下文上限。
func (c *Client) ContextWindowTokens() int {
	if config.Config != nil && config.Config.LLM.ContextWindow > 0 {
		return config.Config.LLM.ContextWindow
	}
	return DefaultContextWindow(c.model)
}

// ContextCompressRatioValue 会话压缩触发比例（默认 0.85）。
func ContextCompressRatioValue() float64 {
	if config.Config != nil && config.Config.Evolution.ContextCompressRatio > 0 &&
		config.Config.Evolution.ContextCompressRatio <= 1 {
		return config.Config.Evolution.ContextCompressRatio
	}
	return 0.85
}

// ContextCompressThreshold 达到该 token 数时触发会话压缩。
func ContextCompressThreshold(window int) int {
	if window <= 0 {
		window = contextWindow1M
	}
	return int(float64(window) * ContextCompressRatioValue())
}

// EstimatedChatInputTokens 估算发往 API 前的输入 token（含 boot-leader + brain 节选注入）。
func (c *Client) EstimatedChatInputTokens(messages []Message, tools []Tool) int {
	wired := withBootLeaderSystemMessage(messages)
	n := estimateMessagesTokens(wired)
	n += estimateToolsTokens(tools)
	// 预留生成空间（与 max_tokens 无关，只避免把窗口算满）
	if config.Config != nil && config.Config.LLM.MaxTokens > 0 {
		n += config.Config.LLM.MaxTokens / 4
	} else {
		n += 500
	}
	return n
}

func estimateMessagesTokens(msgs []Message) int {
	var chars int
	for _, m := range msgs {
		chars += utf8.RuneCountInString(m.Content)
		chars += utf8.RuneCountInString(m.Name)
		chars += utf8.RuneCountInString(m.ToolCallID)
		for _, tc := range m.ToolCalls {
			chars += utf8.RuneCountInString(tc.Function.Name)
			chars += utf8.RuneCountInString(tc.Function.Arguments)
		}
		chars += 16
	}
	if chars <= 0 {
		return 0
	}
	return int(float64(chars) / charsPerTokenEstimate)
}

func estimateToolsTokens(tools []Tool) int {
	if len(tools) == 0 {
		return 0
	}
	b, err := json.Marshal(tools)
	if err != nil {
		return 400
	}
	return int(float64(len(b)) / charsPerTokenEstimate)
}
