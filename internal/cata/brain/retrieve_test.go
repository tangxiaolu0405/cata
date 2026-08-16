package brain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTokenizeForRetrieval(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"涨停板选股", []string{"涨停", "停板", "板选", "选股"}},
		{"stock screen", []string{"stock", "screen"}},
		{"选股 stock", []string{"选股", "stock"}},
		{"a", []string{}}, // 单字母英文不产出 token
	}
	for _, c := range cases {
		got := tokenizeForRetrieval(c.in)
		if !equalStringSlices(got, c.want) {
			t.Errorf("tokenizeForRetrieval(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestIsRetrievableSource(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"memory/long/workflow_sop.md", true},
		{"memory/archive/consolidated-1.md", true},
		{"skills/foo/SKILL.md", true},
		{"brain/memory/long/foo.md", true}, // 容忍 brain/ 前缀
		{"persona.local.md", false},
		{"modes/_default/persona.md", false},
		{"memory/index.json", false},
		{"meta.json", false},
	}
	for _, c := range cases {
		if got := isRetrievableSource(c.in); got != c.want {
			t.Errorf("isRetrievableSource(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestScoreEntry(t *testing.T) {
	q := tokenizeForRetrieval("涨停板选股")
	hit := IndexEntry{
		Source:   "memory/long/workflow_sop.md",
		Summary:  "涨停板选股流程",
		Keywords: []string{"涨停板", "选股", "workflow"},
		Category: "procedure",
		Priority: 8,
	}
	if s := scoreEntry(q, hit); s <= 0 {
		t.Fatalf("expected positive score, got %d", s)
	}
	miss := IndexEntry{
		Source:   "memory/long/foo.md",
		Summary:  "browser 抓取踩坑",
		Keywords: []string{"browser", "抓取"},
		Category: "fact",
		Priority: 5,
	}
	if s := scoreEntry(q, miss); s != 0 {
		t.Fatalf("expected zero score for unrelated entry, got %d", s)
	}
}

func TestBestMatchingSection(t *testing.T) {
	body := "## 涨停板选股\n\n1. 筛选涨停股\n2. 检查连板\n\n## 无关节\n\nxxx\n"
	got := bestMatchingSection(body, tokenizeForRetrieval("涨停板选股"))
	if !strings.Contains(got, "涨停板选股") {
		t.Fatalf("expected matching section, got %q", got)
	}
}

func TestRetrieveRelevantMemoryEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	proj := filepath.Join(home, "proj")
	w := &Workspace{ID: "ws1", RootPath: proj, ActiveMode: "_default"}
	cell := w.Dir()

	if err := os.MkdirAll(filepath.Join(cell, "memory", "long"), 0755); err != nil {
		t.Fatal(err)
	}
	idx := &MemoryIndex{Version: memoryIndexVersion, Entries: []IndexEntry{
		{ID: "sop", Source: "memory/long/workflow_sop.md", Summary: "涨停板选股流程", Keywords: []string{"涨停板", "选股", "workflow"}, Category: "procedure", Priority: 8},
		{ID: "fail", Source: "memory/long/sub-agent-failures.md", Summary: "browser 抓取踩坑", Keywords: []string{"browser", "抓取"}, Category: "fact", Priority: 5},
	}}
	if err := SaveMemoryIndexFor(w, idx); err != nil {
		t.Fatal(err)
	}
	sop := "## 涨停板选股\n\n1. 筛选涨停股\n2. 检查连板\n\n## 无关节\n\nxxx\n"
	if err := os.WriteFile(filepath.Join(cell, "memory", "long", "workflow_sop.md"), []byte(sop), 0644); err != nil {
		t.Fatal(err)
	}

	hits := RetrieveRelevantMemory(w, "帮我做涨停板选股", 2)
	if len(hits) == 0 {
		t.Fatal("expected at least one hit")
	}
	if hits[0].Source != "memory/long/workflow_sop.md" {
		t.Fatalf("expected workflow_sop hit first, got %q", hits[0].Source)
	}
	if !strings.Contains(hits[0].Snippet, "涨停板") {
		t.Fatalf("expected snippet containing 涨停板, got %q", hits[0].Snippet)
	}
}

func TestRetrievedMemorySystemBlockSkipsMinimal(t *testing.T) {
	if got := RetrievedMemorySystemBlock(nil, PromptProfileMinimal, "anything"); got != "" {
		t.Fatalf("minimal profile should not retrieve, got %q", got)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
