package llm

import "strings"

// isResponsesAPIURL reports whether the endpoint is OpenAI/xAI Responses API
// (/v1/responses), not Chat Completions.
func isResponsesAPIURL(apiURL string) bool {
	u := strings.ToLower(TrimAPIURL(apiURL))
	if u == "" || strings.Contains(u, "/chat/completions") {
		return false
	}
	return strings.HasSuffix(u, "/responses") || strings.Contains(u, "/responses?")
}

// chatCompletionsSiblingURL maps .../responses → .../chat/completions when possible.
func chatCompletionsSiblingURL(apiURL string) string {
	u := TrimAPIURL(apiURL)
	if !isResponsesAPIURL(u) {
		return ""
	}
	lower := strings.ToLower(u)
	idx := strings.LastIndex(lower, "/responses")
	if idx < 0 {
		return ""
	}
	return u[:idx] + "/chat/completions"
}

// CandidateAPIURLs 返回按 api_format 尝试的 URL 列表：先用配置原样，再补默认路径（若尚未包含）。
// 对 /v1/responses 另试同前缀的 /chat/completions（字段模型不同，可回退）。
func CandidateAPIURLs(apiFormat, apiURL string) []string {
	u := TrimAPIURL(apiURL)
	if u == "" {
		return nil
	}
	add := func(dst *[]string, x string) {
		x = TrimAPIURL(x)
		if x == "" {
			return
		}
		for _, e := range *dst {
			if e == x {
				return
			}
		}
		*dst = append(*dst, x)
	}

	var out []string
	add(&out, u)

	switch ResolveAPIFormat(apiFormat, apiURL, "") {
	case APIFormatAnthropic:
		if !strings.Contains(u, "/v1/messages") {
			add(&out, u+"/v1/messages")
		}
	default:
		if isResponsesAPIURL(u) {
			if sib := chatCompletionsSiblingURL(u); sib != "" {
				add(&out, sib)
			}
		} else if !strings.Contains(u, "/chat/completions") {
			add(&out, u+"/chat/completions")
		}
	}
	return out
}

// TrimAPIURL 只去掉首尾空白与末尾斜杠，不拼接路径。
func TrimAPIURL(apiURL string) string {
	return strings.TrimRight(strings.TrimSpace(apiURL), "/")
}
