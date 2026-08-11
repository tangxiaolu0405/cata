package client

import (
	"strings"
	"testing"
)

func newTestModel() *model {
	mm := newModel(&session{}, "/tmp/proj")
	mm.width = 120
	mm.height = 30
	mm.displayMode = ""
	return &mm
}

// TestLogEventGoesToStatusNotMainArea log 事件不再刷主区，只进概要 + 运行细节。
func TestLogEventGoesToStatusNotMainArea(t *testing.T) {
	m := newTestModel()
	msg := "[boot] 首次消息诊断（排障用，真实日志见 cata-server.log）\nws=w1 focus=/tmp/proj mode=dev"
	m.handleStream(streamEvent{kind: "log", raw: map[string]any{
		"level":   "info",
		"summary": "[boot] 首次消息诊断：ws=w1 model=m1 key=✓",
		"message": msg,
	}})

	if strings.Contains(m.log, "首次消息诊断") {
		t.Fatalf("log 事件不应写入主区：\n%q", m.log)
	}
	if m.stats.runSummary == "" {
		t.Fatalf("runSummary 未设置")
	}
	if !strings.Contains(m.stats.runSummary, "ws=w1") {
		t.Fatalf("runSummary=%q，期望包含 ws=w1", m.stats.runSummary)
	}
	if len(m.stats.runDetails) == 0 || !strings.Contains(strings.Join(m.stats.runDetails, "\n"), "ws=w1") {
		t.Fatalf("runDetails 未记录完整消息：%v", m.stats.runDetails)
	}
}

// TestLogEventSummaryFallback 无 summary 字段时取 message 首行作概要。
func TestLogEventSummaryFallback(t *testing.T) {
	m := newTestModel()
	m.handleStream(streamEvent{kind: "log", raw: map[string]any{
		"message": "第一行概要\n第二行详情",
	}})
	if !strings.HasPrefix(m.stats.runSummary, "第一行概要") {
		t.Fatalf("runSummary=%q，期望以首行开头", m.stats.runSummary)
	}
}

// TestRunSidebarSection 侧栏「运行」区展示一行概要 + 一行运行状态。
func TestRunSidebarSection(t *testing.T) {
	m := newTestModel()
	m.handleStream(streamEvent{kind: "log", raw: map[string]any{
		"summary": "[boot] 首次消息诊断：ws=w1 model=m1 key=✓",
		"message": "[boot] 首次消息诊断\n细节",
	}})
	m.streaming = true
	m.stats.state = "model round 2"
	m.stats.lastTool = "run_command"
	m.syncSidebarViewport()

	text := m.sidebarText()
	if !strings.Contains(text, "运行") {
		t.Fatalf("侧栏缺少「运行」区：\n%s", text)
	}
	if !strings.Contains(text, "概要") || !strings.Contains(text, "状态") {
		t.Fatalf("侧栏缺少概要/状态行：\n%s", text)
	}
	if !strings.Contains(text, "model round 2") {
		t.Fatalf("状态行未反映任务变化：\n%s", text)
	}
}

// TestStatusLineClickDetection 「状态」行可被点击识别（打开运行详情）。
func TestStatusLineClickDetection(t *testing.T) {
	m := newTestModel()
	m.handleStream(streamEvent{kind: "log", raw: map[string]any{
		"message": "[boot] 首次消息诊断\n细节",
	}})
	m.syncSidebarViewport()
	lines := strings.Split(m.sidebarText(), "\n")
	idx := -1
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "状态") {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("未找到状态行：\n%v", lines)
	}
	if !m.statusLineAtSidebarLine(idx) {
		t.Fatalf("状态行第 %d 行应可点击", idx)
	}
	if m.statusLineAtSidebarLine(idx - 1) {
		t.Fatalf("非状态行不应误判可点击")
	}
}

// TestOpenStatusViewOverlay 点击状态 → 运行详情 overlay 包含运行细节。
func TestOpenStatusViewOverlay(t *testing.T) {
	m := newTestModel()
	m.appendRunDetail("▸ run_command")
	m.appendRunDetail("• model round 2")
	nm, _ := m.openStatusView()
	mm, ok := nm.(*model)
	if !ok || mm.overlay == nil || mm.overlay.mode != overlayStatusView {
		t.Fatalf("overlay 未打开为 status view")
	}
	rendered := mm.renderStatusOverlay()
	if !strings.Contains(rendered, "运行详情") || !strings.Contains(rendered, "run_command") {
		t.Fatalf("运行详情内容缺失：\n%s", rendered)
	}
}
