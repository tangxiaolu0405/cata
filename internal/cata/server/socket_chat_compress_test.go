package server

import (
	"context"
	"testing"

	"cata/internal/cata/config"
	"cata/internal/llm"
)

// testClientWithTallEstimates 构造估算很高 / 很低的客户端，便于测压缩预算阈值。
// 注意：llm.EstimatedChatInputTokens 会读全局 config.Config（角色卡片注入等），此处仅用
// EstimatedChatInputTokens 的字符估算部分做预算控制测试，不断言精确 token 值。
func testChatClient(t *testing.T) *llm.Client {
	t.Helper()
	prev := config.Config
	config.Config = &config.AppConfig{LLM: config.LLMConfig{
		Model:   "test-model",
		APIKey:  "test-key-not-real",
		Models:  map[string]string{"chat": "test-model"},
		Enabled: true,
	}}
	t.Cleanup(func() { config.Config = prev })
	return mustChatClient(t)
}

func mustChatClient(t *testing.T) *llm.Client {
	t.Helper()
	c, err := llm.NewClientForRole(llm.RoleChat)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestTrimHistoryDropsOldMediaFirst 验证压缩优先剥离最早的 Media（保留文本），
// 而不是直接裁整条消息；剥完仍超预算才裁消息。
func TestTrimHistoryDropsOldMediaFirst(t *testing.T) {
	c := testChatClient(t)
	ctx := context.Background()

	fill := make([]llm.Message, 0, 8)
	big := make([]byte, 60000) // 长文本，保证估算显著
	for i := range big {
		big[i] = 'a'
	}
	agentText := string(big)
	withImg := make([]byte, 20000)
	for i := range withImg {
		withImg[i] = 'b'
	}
	imgText := string(withImg)

	fill = append(fill,
		llm.Message{Role: "user", Content: imgText, Media: []llm.MediaRef{{ID: "old.png", MIME: "image/png", Data: "QUJD"}}},
		llm.Message{Role: "assistant", Content: agentText},
		llm.Message{Role: "user", Content: agentText, Media: []llm.MediaRef{{ID: "new.png", MIME: "image/png", Data: "QUJD"}}},
	)

	// 超紧预算：应至少剥掉 Media 但可能仍要裁消息；断言结果是合法消息序列（非空、首尾可丢规则内）。
	got := trimHistoryToTokenBudget(ctx, c, fill, nil, 10)
	if len(got) == 0 {
		t.Fatal("trim produced empty history")
	}
	// 任何残留 user 轮都不应再带 Media（阶段一应已剥完或裁掉）。
	for i, m := range got {
		if m.Role == "user" && len(m.Media) > 0 {
			t.Fatalf("media left at index %d: %+v", i, m.Media)
		}
	}
}

// TestFirstMediaUserIndex 定位最早带 Media 的 user 消息。
func TestFirstMediaUserIndex(t *testing.T) {
	msgs := []llm.Message{
		{Role: "system", Content: "s"},
		{Role: "user", Content: "hi", Media: []llm.MediaRef{{ID: "a.png", MIME: "image/png"}}},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: "hi2", Media: []llm.MediaRef{{ID: "b.png", MIME: "image/png"}}},
	}
	if got := firstMediaUserIndex(msgs); got != 1 {
		t.Fatalf("got %d want 1", got)
	}
	noMedia := []llm.Message{{Role: "user", Content: "x"}, {Role: "assistant", Content: "y"}}
	if got := firstMediaUserIndex(noMedia); got != -1 {
		t.Fatalf("got %d want -1", got)
	}
}

// TestFirstDroppableIndex 可丢消息下标：user/assistant/tool，跳过 system。
func TestFirstDroppableIndex(t *testing.T) {
	msgs := []llm.Message{
		{Role: "system", Content: "s"},
		{Role: "assistant", Content: "a"},
	}
	if got := firstDroppableIndex(msgs); got != 1 {
		t.Fatalf("got %d want 1", got)
	}
}
