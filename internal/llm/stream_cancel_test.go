package llm

import (
	"context"
	"errors"
	"testing"
)

// TestNonStreamFallbackRound_CancelledContext 验证 Ctrl+C（ctx 已取消）时非流式回退
// 直接返回 context.Canceled，不发起新的 HTTP 请求、也不进入可重试错误串。
func TestNonStreamFallbackRound_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, _, _, err := (&Client{}).nonStreamFallbackRound(ctx, nil, nil, "auto", 0, 0, streamRoundFlags{}, nil, nil, StreamUsage{}, "test")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if IsRetryableChatError(err) {
		t.Fatalf("cancelled error must not be retryable, got %v", err)
	}
}
