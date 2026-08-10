package evolve

import (
	"strings"
	"testing"
)

func TestCompactMarkdown_dedupeSections(t *testing.T) {
	body := []byte("# Persona\n\n## Preferences\n\nold\n\n## Who I am\n\nidentity\n\n## Preferences\n\nnew pref\n")
	out := string(CompactMarkdown(body))
	if strings.Contains(out, "old") {
		t.Fatalf("expected duplicate section removed:\n%s", out)
	}
	if !strings.Contains(out, "new pref") || !strings.Contains(out, "identity") {
		t.Fatalf("unexpected:\n%s", out)
	}
}

func TestCompactMarkdown_dedupeParagraphs(t *testing.T) {
	body := []byte("## Notes\n\nsame fact\n\nsame fact\n\ndifferent\n")
	out := string(CompactMarkdown(body))
	if strings.Count(out, "same fact") != 1 {
		t.Fatalf("expected one paragraph:\n%s", out)
	}
	if !strings.Contains(out, "different") {
		t.Fatalf("expected different kept:\n%s", out)
	}
}

func TestIsProjectContentRel(t *testing.T) {
	cases := map[string]bool{
		"persona.local.md":                 true,
		"modes/_default/persona.md":        true,
		"modes/foo/behavior.md":            true,
		"memory/long/x.md":                 false,
		"modes/_default/capabilities.yaml": false,
	}
	for rel, want := range cases {
		if got := IsProjectContentRel(rel); got != want {
			t.Fatalf("IsProjectContentRel(%q)=%v want %v", rel, got, want)
		}
	}
}

func TestFilterUpdates_allowsAppendOnPersonaWhenSubstantive(t *testing.T) {
	out := filterUpdates([]DocUpdate{{
		Path:    "modes/_default/persona.md",
		Mode:    "append",
		Content: "this is a long enough append that passes min length filter",
	}})
	if len(out) != 1 {
		t.Fatalf("expected append on persona allowed, got %d", len(out))
	}
}
