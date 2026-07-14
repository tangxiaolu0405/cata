package llm

import (
	"strings"

	"cata/internal/cata/config"
)

const (
	APIFormatOpenAI     = "openai"
	APIFormatAnthropic  = "anthropic"
	DefaultAnthropicURL = "https://api.anthropic.com/v1/messages"
)

// ResolveAPIFormat 从配置或参数解析协议格式。
func ResolveAPIFormat(explicit, apiURL, providerLabel string) string {
	if config.Config != nil && strings.TrimSpace(explicit) == "" {
		explicit = config.Config.LLM.APIFormat
		if strings.TrimSpace(apiURL) == "" {
			apiURL = config.Config.LLM.APIURL
		}
		if strings.TrimSpace(providerLabel) == "" {
			providerLabel = config.Config.LLM.Provider
		}
	}
	return config.NormalizeAPIFormat(explicit, apiURL, providerLabel)
}
