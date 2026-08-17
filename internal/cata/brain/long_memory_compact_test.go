package brain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsLearningNoise(t *testing.T) {
	if !isLearningNoise("No learning as no new facts.") {
		t.Fatal("expected noise")
	}
	if !isLearningNoise("Periodic consolidation with no new information") {
		t.Fatal("expected noise")
	}
	if isLearningNoise("用户明确要求抓取用Windows浏览器、保存用WSL bash heredoc") {
		t.Fatal("expected signal")
	}
}

func TestCompactLearningPlaybookContent_dedupesAndDropsNoise(t *testing.T) {
	raw := `# Evolution learnings

## 2026-05-20T15:05:05

- User prefers CSV output to D:\stock

## 2026-05-20T15:09:39

- Periodic consolidation triggered with no new short-term facts

## 2026-05-20T15:10:06

- User prefers CSV output to D:\stock

## 2026-05-21T16:52:11

- Session captured limit-up analysis with local export to D:\stock
`
	out := compactLearningPlaybookContent(raw)
	if strings.Contains(out, "Periodic consolidation") {
		t.Fatalf("noise not removed: %q", out)
	}
	if strings.Count(out, "User prefers CSV") > 1 {
		t.Fatalf("duplicate not merged: %q", out)
	}
	if !strings.Contains(out, "limit-up analysis") {
		t.Fatalf("signal missing: %q", out)
	}
}

func TestLearningBulletsSimilar(t *testing.T) {
	// 真实重复：同一「daban-fupan 模式结晶」换措辞记录。
	cases := []struct {
		a, b string
		want bool
	}{
		{
			"结晶daban-fupan专职模式，接管复盘流水线+7项checklist，_default保留通用调度",
			"daban-fupan模式已结晶：专职复盘+选股，含7项checklist、因子迭代、命中率追踪；已从_default剥离独立维护",
			true,
		},
		{
			"结晶 daban-fupan 专职模式，接管复盘流水线与7项checklist，已执行2次复盘，命中率50%。",
			"daban-fupan模式已结晶，专职复盘+选股，含7项checklist、因子迭代",
			true,
		},
		{
			"CSV文件被引号包裹成单行字符串时，需先修复格式再解析",
			"复盘输出优化清单7项已固化到behavior.md，每次复盘自动检查",
			false,
		},
	}
	for _, c := range cases {
		if got := learningBulletsSimilar(c.a, c.b); got != c.want {
			t.Errorf("similar(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestCompactLearningPlaybookContent_semanticDedup(t *testing.T) {
	raw := `# Evolution learnings

## 2026-07-28

- 结晶daban-fupan专职模式，接管复盘流水线+7项checklist，_default保留通用调度

## 2026-07-29

- daban-fupan模式已结晶：专职复盘+选股，含7项checklist、因子迭代、命中率追踪；已从_default剥离独立维护

## 2026-07-30

- CSV文件被引号包裹成单行字符串时，需先修复格式再解析
`
	out := compactLearningPlaybookContent(raw)
	if strings.Count(out, "daban-fupan") > 1 {
		t.Fatalf("semantic duplicate not merged: %q", out)
	}
	if !strings.Contains(out, "CSV") {
		t.Fatalf("distinct learning lost: %q", out)
	}
}

func TestCompactLongMemory_archivesBulk(t *testing.T) {
	ws, _ := testWorkspace(t, "compact-ws")
	longDir := ws.LongTermDir()
	if err := os.MkdirAll(longDir, 0755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(longDir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("consolidated-2026-06-30-160757.md", "# archive me\n")
	write("2026-07-10-summary.md", "## summary\n\n- fact\n")
	write("workflow_sop.md", "## sop\n")
	write("learnings.md", "# Evolution learnings\n\n## 2026-07-01\n\n- 用户要求保留星星优先级格式输出\n")
	if err := os.MkdirAll(filepath.Dir(ws.MemoryIndexPath()), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ws.MemoryIndexPath(), []byte(`{"version":1,"entries":[{"id":"c1","source":"memory/long/consolidated-2026-06-30-160757.md","summary":"x","category":"episodic","priority":4}]}`), 0644); err != nil {
		t.Fatal(err)
	}

	SetActive(ws)
	defer SetActive(nil)
	if err := CompactLongMemoryFor(ws); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(longDir, "consolidated-2026-06-30-160757.md")); !os.IsNotExist(err) {
		t.Fatal("consolidated should be archived")
	}
	if _, err := os.Stat(ws.Path(RelMemoryLongSessionNotes)); err != nil {
		t.Fatalf("session-notes missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(longDir, "workflow_sop.md")); err != nil {
		t.Fatal("workflow_sop should remain")
	}
	entries, _ := os.ReadDir(longDir)
	if len(entries) > 6 {
		t.Fatalf("too many files left in long/: %d", len(entries))
	}
}
