package brain

import (
	"os"
	"path/filepath"
	"testing"
)

func writeShortTerm(w *Workspace, body string) {
	if err := os.MkdirAll(filepath.Join(w.Dir(), "memory", "short"), 0755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(w.ShortTermPath(), []byte(body), 0644); err != nil {
		panic(err)
	}
}

func TestDetectUserCorrection(t *testing.T) {
	t.Setenv("CATA_HOME", t.TempDir())
	w := &Workspace{ID: "corr-ws", ActiveMode: ModeDefaultID}

	// 最近的 user 消息含明确纠正。
	writeShortTerm(w, `# Short-term

## 2026-08-16T10:00:00+08:00

**User:** 帮我看看涨停板

**Assistant:** 好的...

## 2026-08-16T10:05:00+08:00

**User:** 不对，不是这样，应该是按连板高度

**Assistant:** 抱歉，我修正一下
`)
	ok, ts := DetectUserCorrection(w)
	if !ok {
		t.Fatal("expected correction signal")
	}
	if ts == "" {
		t.Fatal("expected correction timestamp")
	}
}

func TestDetectNoCorrection(t *testing.T) {
	t.Setenv("CATA_HOME", t.TempDir())
	w := &Workspace{ID: "corr-ws2", ActiveMode: ModeDefaultID}
	writeShortTerm(w, `# Short-term

## 2026-08-16T10:00:00+08:00

**User:** 继续分析涨停板

**Assistant:** 好的，继续
`)
	if ok, _ := DetectUserCorrection(w); ok {
		t.Fatal("expected no correction for normal user message")
	}
}

func TestScanShortTermCorrection_multilineUser(t *testing.T) {
	body := `# Short-term

## 2026-08-16T10:00:00+08:00

**User:** 明天记得
提醒我买股票

**Assistant:** 好的
`
	ok, ts := scanShortTermCorrection(body)
	if ok {
		t.Fatal("multiline normal user should not be correction")
	}
	if ts != "" {
		t.Fatal("no correction timestamp expected")
	}
}
