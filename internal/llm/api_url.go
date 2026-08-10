package llm

import "strings"

const (
	openAIChatCompletionsPath = "/chat/completions"
	anthropicMessagesPath     = "/v1/messages"
)

// AppendAPIFormatPath 按 api_format 为 base URL 拼接默认路径（用于候选探测，不强制写回配置）。
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
		if strings.Contains(u, openAIChatCompletionsPath) || isResponsesAPIURL(u) {
			return u
		}
		return u + openAIChatCompletionsPath
	}
}

// NormalizeAPIURL 只规范化空白/尾斜杠；路径由运行时 CandidateAPIURLs 探测并记住。
func NormalizeAPIURL(_, apiURL string) string {
	return TrimAPIURL(apiURL)
}
