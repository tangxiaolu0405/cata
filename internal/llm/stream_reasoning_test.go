package llm

import (
	"strings"
	"testing"
)

// TestReadOpenAIChatStream_ForwardsReasoningDeltas 验证 reasoning_content 增量会实时交给
// onReasoning（--show-thinking 依赖），正文增量仍走 onDelta。
func TestReadOpenAIChatStream_ForwardsReasoningDeltas(t *testing.T) {
	sse := "" +
		"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"先分析\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"再执行\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"结果\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"

	var gotReasoning, gotContent []string
	content, reasoning, _, _, _, err := ReadOpenAIChatStream(strings.NewReader(sse),
		func(s string) error { gotContent = append(gotContent, s); return nil },
		func(s string) error { gotReasoning = append(gotReasoning, s); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if reasoning != "先分析再执行" || content != "结果" {
		t.Fatalf("reasoning=%q content=%q", reasoning, content)
	}
	if len(gotReasoning) != 2 || gotReasoning[0] != "先分析" || gotReasoning[1] != "再执行" {
		t.Fatalf("onReasoning deltas=%v", gotReasoning)
	}
	if len(gotContent) != 1 || gotContent[0] != "结果" {
		t.Fatalf("onDelta deltas=%v", gotContent)
	}
}

// TestReadOpenAIChatStream_ReasoningOnlyInFinalMessage 兼容网关只在最后一帧 message 带
// reasoning_content：应一次性补发 onReasoning，且不重复。
func TestReadOpenAIChatStream_ReasoningOnlyInFinalMessage(t *testing.T) {
	sse := "" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"message\":{\"content\":\"x\",\"reasoning_content\":\"末尾思考\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"

	var gotReasoning []string
	_, reasoning, _, _, _, err := ReadOpenAIChatStream(strings.NewReader(sse), nil,
		func(s string) error { gotReasoning = append(gotReasoning, s); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if reasoning != "末尾思考" {
		t.Fatalf("reasoning=%q", reasoning)
	}
	if len(gotReasoning) != 1 || gotReasoning[0] != "末尾思考" {
		t.Fatalf("onReasoning=%v, want single final emit", gotReasoning)
	}
}

// TestSendAssistantDelta_NoReasoningDuplicate 验证 onReasoning 已下发推理时，onDelta 不再收到
// assistantText 的 reasoning 回退（避免 TUI 思考块与正文重复）；未开启 onReasoning 时行为不变。
func TestSendAssistantDelta_NoReasoningDuplicate(t *testing.T) {
	// 只有推理、无正文：onReasoning 已发 → onDelta 不应再收到。
	var got []string
	sendAssistantDelta(func(s string) error { got = append(got, s); return nil },
		func(s string) error { return nil }, "", "secret-reasoning")
	if len(got) != 0 {
		t.Fatalf("onDelta 不应收到 reasoning 回退：%v", got)
	}

	// 有正文 + 推理：onDelta 只收正文。
	got = nil
	sendAssistantDelta(func(s string) error { got = append(got, s); return nil },
		func(s string) error { return nil }, "answer", "secret")
	if len(got) != 1 || got[0] != "answer" {
		t.Fatalf("onDelta 应只收正文：%v", got)
	}

	// onReasoning 为 nil（默认）：保持原 assistantText 回退，正文/推理照旧走 onDelta。
	got = nil
	sendAssistantDelta(func(s string) error { got = append(got, s); return nil }, nil, "", "fallback-reasoning")
	if len(got) != 1 || got[0] != "fallback-reasoning" {
		t.Fatalf("默认应回退发 reasoning：%v", got)
	}
}
