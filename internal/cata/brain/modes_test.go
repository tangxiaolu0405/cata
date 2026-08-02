package brain

import "testing"

func TestNormalizeModeID(t *testing.T) {
	cases := map[string]string{
		"":              ModeDefaultID,
		"default":       ModeDefaultID,
		"Default":       ModeDefaultID,
		"_default":      ModeDefaultID,
		"_orchestrator": ModeDefaultID,
		"work":          "work",
		" coding ":      "coding",
	}
	for in, want := range cases {
		if got := NormalizeModeID(in); got != want {
			t.Fatalf("NormalizeModeID(%q)=%q want %q", in, got, want)
		}
	}
}

func TestNormalizeModePathRel(t *testing.T) {
	cases := map[string]string{
		"modes/default/persona.md":          "modes/_default/persona.md",
		"modes/Default/constraints.md":      "modes/_default/constraints.md",
		"modes/_default/persona.md":         "modes/_default/persona.md",
		"modes/_orchestrator/persona.md":    "modes/_default/persona.md",
		"modes/work/persona.md":             "modes/work/persona.md",
	}
	for in, want := range cases {
		if got := normalizeModePathRel(in); got != want {
			t.Fatalf("normalizeModePathRel(%q)=%q want %q", in, got, want)
		}
	}
}

func TestNormalizeEvolveUpdatePath_defaultMode(t *testing.T) {
	got, err := NormalizeEvolveUpdatePath("modes/default/persona.md")
	if err != nil {
		t.Fatal(err)
	}
	want := "modes/_default/persona.md"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
