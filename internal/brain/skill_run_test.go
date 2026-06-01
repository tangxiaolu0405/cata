package brain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSkillIDFromRel(t *testing.T) {
	if got := ParseSkillIDFromRel("skills/zhangtingban-lianban/SKILL.md"); got != "zhangtingban-lianban" {
		t.Fatalf("got %q", got)
	}
	if got := ParseSkillIDFromRel("memory/long/x.md"); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestLoadSkillManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileSkillManifest)
	if err := os.WriteFile(path, []byte("runner: python\nentry: hello.py\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadSkillManifest(dir)
	if err != nil || m.Entry != "hello.py" {
		t.Fatalf("m=%v err=%v", m, err)
	}
}
