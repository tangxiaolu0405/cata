package brain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionSpill_WriteAndClear(t *testing.T) {
	dir := t.TempDir()
	rel, err := WriteSessionSpill(dir, "sess-abc", SpillFileName(1, "run_command"), "hello body")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rel, SessionSpillRelPrefix+"/sess-abc/") {
		t.Fatalf("unexpected rel path: %q", rel)
	}
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("spill file not readable: %v", err)
	}
	if string(data) != "hello body" {
		t.Fatalf("content mismatch: %q", string(data))
	}

	if err := ClearSessionSpill(dir, "sess-abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(abs)); !os.IsNotExist(err) {
		t.Fatalf("session dir should be gone, err=%v", err)
	}
}

func TestSessionSpill_SanitizesSessionID(t *testing.T) {
	dir := t.TempDir()
	// 含路径分隔符/点号的 id 必须被归一，不能越出 session-spill 根。
	rel, err := WriteSessionSpill(dir, "../../etc/passwd", SpillFileName(1, "x"), "b")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rel, "..") {
		t.Fatalf("session id not sanitized: %q", rel)
	}
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	root := filepath.Join(dir, ProjectCataDir, DirSessionSpill)
	if !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		t.Fatalf("spill escaped root: %q (root %q)", abs, root)
	}
}

func TestSpillSummary_ContainsPathAndPreview(t *testing.T) {
	body := "\n\n第一行有内容\n第二行\n"
	s := SpillSummary("a/b/c.txt", body, len(body))
	if !strings.Contains(s, "a/b/c.txt") {
		t.Fatalf("summary missing path: %q", s)
	}
	if !strings.Contains(s, "第一行有内容") {
		t.Fatalf("summary missing preview: %q", s)
	}
	if !strings.HasPrefix(s, "[已摘要]") {
		t.Fatalf("summary marker missing: %q", s)
	}
}

func TestSpillFileName(t *testing.T) {
	if got := SpillFileName(7, "run_command"); got != "007-run_command.txt" {
		t.Fatalf("got %q", got)
	}
	if got := SpillFileName(1, ""); got != "001-message.txt" {
		t.Fatalf("empty tool name should fall back: %q", got)
	}
	if got := SpillFileName(1, "a/b c"); got != "001-abc.txt" {
		t.Fatalf("tool name should be sanitized: %q", got)
	}
}
