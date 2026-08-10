package llm

import (
	"context"
	"errors"
	"net"
	"strings"
)

// IsRetryableChatError 是否为可重试的 LLM 错误（超时、临时网络问题等）。
func IsRetryableChatError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "timeout") ||
		strings.Contains(s, "deadline exceeded") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "eof") ||
		strings.Contains(s, "temporary failure") ||
		strings.Contains(s, "empty llm response") ||
		strings.Contains(s, "non-stream fallback failed")
}
