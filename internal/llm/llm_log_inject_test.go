package llm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cata/internal/cata/brain"
)

// TestAppendLLMLogSeesInjectedStaticBlocks 回归：llm.log 必须记录「实际出站」的 messages，
// 即身份 + 检索 + 脑子节选（路径块/项目内容）都在里面。
// 历史 bug：组装结果只写进 buildHTTPChatRequest 的局部 req 副本，
// 调用方仍用注入前的原始 history 写日志 → boot-leader/brain-excerpt 恒为 null。
func TestAppendLLMLogSeesInjectedStaticBlocks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	logFile := filepath.Join(home, "llm.log")
	t.Setenv("LLM_LOG_FILE", logFile)

	// 最小 workspace：focus_path/.cata/modes/_default/persona.md 有内容，
	// 保证 terminalProjectContentExcerpt 能产出【Cata 项目内容】块。
	root := filepath.Join(home, "proj")
	modeDir := filepath.Join(root, ".cata", "modes", brain.ModeDefaultID)
	if err := os.MkdirAll(modeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modeDir, "persona.md"),
		[]byte("# Persona\n\n我是测试人格。\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ws := &brain.Workspace{ID: "test-ws", RootPath: root, ActiveMode: brain.ModeDefaultID}
	cc := &brain.ChatContext{
		WS:        ws,
		OutputCwd: root,
		Runtime:   &brain.RuntimeEnv{OS: "darwin", Shell: "zsh"},
		Profile:   brain.PromptProfileTask,
	}
	ctx := brain.WithChatContext(context.Background(), cc)

	c := &Client{apiURL: "http://example.com", apiKey: "k", model: "m"}
	// 走与 buildHTTPChatRequest 相同的组装入口（adapter 缺失时无法真正 BuildRequest，
	// 但注入链与日志链共用同一份 effective messages）。
	effective := c.assembleSystemForRole(ctx, []Message{{Role: "user", Content: "hi"}}, brain.PromptProfileTask)
	if len(effective) < 3 || effective[0].Role != "system" {
		t.Fatalf("expected identity + brain + user, got %+v", effective)
	}
	// out[0] = 身份卡片；out[1] = 脑子节选（路径块 + 项目内容）。
	var excerpt string
	for _, m := range effective {
		if m.Role == "system" && brain.BrainExcerptInjected(strings.TrimSpace(m.Content)) {
			excerpt = m.Content
			break
		}
	}
	if excerpt == "" {
		t.Fatalf("no brain excerpt among injected system messages: %+v", effective)
	}

	c.appendLLMLog(ChatRequest{}, effective, nil, "", "reply", nil, nil)
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read llm.log: %v", err)
	}
	if strings.Contains(string(data), `"brain-excerpt":null`) {
		t.Fatalf("llm.log brain-excerpt is null — log not using effective messages: %s", string(data))
	}
	if !strings.Contains(string(data), "brain-excerpt") {
		t.Fatalf("llm.log missing brain-excerpt component: %s", string(data))
	}
}
