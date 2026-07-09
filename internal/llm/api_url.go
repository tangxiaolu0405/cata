package llm

import "strings"

const (
	openAIChatCompletionsPath = "/chat/completions"
	anthropicMessagesPath       = "/v1/messages"
)

// AppendAPIFormatPath 按 api_format 为 base URL 拼接默认路径（不按域名匹配）。
// openai → /chat/completions；anthropic → /v1/messages。
// URL 中已含对应路径片段时不再追加。
func AppendAPIFormatPath(apiFormat, apiURL string) string {
	u := strings.TrimRight(strings.TrimSpace(apiURL), "/")
	if u == "" {
		return apiURL
	}
	switch ResolveAPIFormat(apiFormat, apiURL, "") {
	case APIFormatAnthropic:
		if strings.Contains(u, anthropicMessagesPath) {
			return u
		}
		return u + anthropicMessagesPath
	default:
		if strings.Contains(u, openAIChatCompletionsPath) {
			return u
		}
		return u + openAIChatCompletionsPath
	}
}

// NormalizeAPIURL 去掉末尾斜杠并按 api_format 补默认路径。
func NormalizeAPIURL(apiFormat, apiURL string) string {
	return AppendAPIFormatPath(apiFormat, apiURL)
}
