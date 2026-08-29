package client

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
)

func TestSplitAttachmentTokens(t *testing.T) {
	cases := []struct {
		in      string
		wantTxt string
		wantP   []string
	}{
		{"看图 @a.png", "看图", []string{"a.png"}},
		{"@/tmp/x.png 分析", "分析", []string{"/tmp/x.png"}},
		{"无附件", "无附件", nil},
		{"@a.png @b.png 对比", "对比", []string{"a.png", "b.png"}},
		{"email@x.com 保持", "email@x.com 保持", nil}, // 非行首 @token，保持原样
	}
	for _, c := range cases {
		txt, paths := splitAttachmentTokens(c.in)
		if txt != c.wantTxt {
			t.Fatalf("split(%q) text=%q want %q", c.in, txt, c.wantTxt)
		}
		if !reflect.DeepEqual(paths, c.wantP) {
			t.Fatalf("split(%q) paths=%v want %v", c.in, paths, c.wantP)
		}
	}
}

// TestHandleAttachQueue 验证 /attach 命令队列行为：
// 追加 / 清空 / 查看 / 数量上限。
func TestHandleAttachQueue(t *testing.T) {
	m := &model{input: newChatTextarea(), vp: viewport.New(80, 20), sidebarVP: newSidebarViewport()}

	// 空队列：/attach list 提示无待发送附件。
	m.handleAttachCmd("/attach")
	if len(m.attachQueue) != 0 {
		t.Fatalf("empty queue changed: %v", m.attachQueue)
	}

	// 追加两个附件。
	m.handleAttachCmd("/attach a.png")
	m.handleAttachCmd("/attach b.png")
	if !reflect.DeepEqual(m.attachQueue, []string{"a.png", "b.png"}) {
		t.Fatalf("queue=%v", m.attachQueue)
	}

	// 数量上限 12：再多追加被截断。
	for i := 0; i < 15; i++ {
		m.handleAttachCmd("/attach f.png")
	}
	if len(m.attachQueue) != 12 {
		t.Fatalf("queue len=%d want 12", len(m.attachQueue))
	}

	// 清空。
	m.handleAttachCmd("/attach clear")
	if len(m.attachQueue) != 0 {
		t.Fatalf("queue not cleared: %v", m.attachQueue)
	}
}

func TestQueueSummary(t *testing.T) {
	s := queueSummary([]string{"a.png", "dir/b.jpg"})
	if !strings.Contains(s, "[1] a.png") || !strings.Contains(s, "[2] b.jpg") {
		t.Fatalf("summary=%q", s)
	}
}
