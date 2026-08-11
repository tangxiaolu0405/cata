package client

import (
	"strings"
	"testing"
)

// TestThinkingEventRendersBlock 开启 --show-thinking 时，thinking 事件渲染「思考中」块，
// 正文 token 到达后自动收块（不粘连）。
func TestThinkingEventRendersBlock(t *testing.T) {
	m := newTestModel()
	m.showThinking = true

	m.handleStream(streamEvent{kind: "thinking", raw: map[string]any{"content": "先分析"}})
	m.handleStream(streamEvent{kind: "thinking", raw: map[string]any{"content": "再执行"}})
	m.handleStream(streamEvent{kind: "token", raw: map[string]any{"content": "最终结果"}})

	if !strings.Contains(m.log, "思考中") {
		t.Fatalf("缺少思考块标题：%q", m.log)
	}
	if !strings.Contains(m.log, "先分析再执行") {
		t.Fatalf("缺少思考内容：%q", m.log)
	}
	if !strings.Contains(m.log, "最终结果") {
		t.Fatalf("缺少正文：%q", m.log)
	}
	if m.thinkingActive {
		t.Fatalf("token 后 thinking 块应收起")
	}
	// 思考内容与正文之间不应粘连成同一段（至少有一个换行分隔）。
	if strings.Contains(m.log, "再执行最终结果") {
		t.Fatalf("思考与正文粘连：%q", m.log)
	}
}

// TestThinkingHiddenWithoutFlag 未开 --show-thinking 时 thinking 事件应被忽略。
func TestThinkingHiddenWithoutFlag(t *testing.T) {
	m := newTestModel()
	m.showThinking = false

	m.handleStream(streamEvent{kind: "thinking", raw: map[string]any{"content": "秘密推理"}})
	m.handleStream(streamEvent{kind: "done", raw: map[string]any{"success": true}})

	if strings.Contains(m.log, "秘密推理") || strings.Contains(m.log, "思考中") {
		t.Fatalf("未开启时不应展示 thinking：%q", m.log)
	}
	if m.thinkingActive {
		t.Fatalf("thinkingActive 不应置位")
	}
}

// TestThinkingClosedOnErrorAndDone 思考块在 error / done 时应收起。
func TestThinkingClosedOnErrorAndDone(t *testing.T) {
	m := newTestModel()
	m.showThinking = true
	m.handleStream(streamEvent{kind: "thinking", raw: map[string]any{"content": "推理中"}})
	if !m.thinkingActive {
		t.Fatalf("thinking 块未开启")
	}
	m.handleStream(streamEvent{kind: "error", raw: map[string]any{"message": "boom"}})
	if m.thinkingActive {
		t.Fatalf("error 后 thinking 块应收起")
	}

	m2 := newTestModel()
	m2.showThinking = true
	m2.handleStream(streamEvent{kind: "thinking", raw: map[string]any{"content": "推理中"}})
	m2.handleStream(streamEvent{kind: "done", raw: map[string]any{"success": true}})
	if m2.thinkingActive {
		t.Fatalf("done 后 thinking 块应收起")
	}
}
