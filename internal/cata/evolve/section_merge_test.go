package evolve

import (
	"strings"
	"testing"
)

func TestMergeMarkdownSection_appendWhenMissing(t *testing.T) {
	body := []byte("# Persona\n\n## Who I am\n\nold\n")
	out := string(mergeMarkdownSection(body, "Preferences", "likes Go"))
	if !strings.Contains(out, "## Preferences") || !strings.Contains(out, "likes Go") {
		t.Fatalf("expected new section appended:\n%s", out)
	}
	if !strings.Contains(out, "## Who I am") {
		t.Fatal("expected existing section kept")
	}
}

func TestMergeMarkdownSection_replaceExisting(t *testing.T) {
	body := []byte("# Persona\n\n## Preferences\n\nold pref\n\n## Who I am\n\nidentity\n")
	out := string(mergeMarkdownSection(body, "Preferences", "new pref"))
	if strings.Contains(out, "old pref") {
		t.Fatalf("expected old section body replaced:\n%s", out)
	}
	if !strings.Contains(out, "new pref") || !strings.Contains(out, "identity") {
		t.Fatalf("expected new pref and other sections:\n%s", out)
	}
}

func TestMergeMarkdownSection_replaceLastSection(t *testing.T) {
	body := []byte("## A\n\none\n\n## B\n\ntwo\n")
	out := string(mergeMarkdownSection(body, "B", "TWO"))
	if strings.Contains(out, "two") {
		t.Fatalf("expected B replaced:\n%s", out)
	}
	if !strings.Contains(out, "TWO") || !strings.Contains(out, "one") {
		t.Fatalf("unexpected:\n%s", out)
	}
}
