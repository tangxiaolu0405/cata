package brain

import (
	"path/filepath"
	"testing"
)

func TestIsHomeBrainRel(t *testing.T) {
	cases := map[string]bool{
		"memory/short/current.md":   true,
		"memory/index.json":         true,
		"meta.json":                 true,
		"evolution_log.json":        true,
		"persona.local.md":          false,
		"modes/_default/persona.md": false,
		"skills/foo/SKILL.md":       false,
		"brain/memory/long/x.md":    true,
		"brain/persona.local.md":    false,
	}
	for rel, want := range cases {
		if got := IsHomeBrainRel(rel); got != want {
			t.Fatalf("IsHomeBrainRel(%q)=%v want %v", rel, got, want)
		}
	}
}

func TestProjectCataPaths(t *testing.T) {
	root := t.TempDir()
	w := &Workspace{ID: "ws", RootPath: root, ActiveMode: ModeDefaultID}
	if got := w.ProjectCataRoot(); got != filepath.Join(root, ProjectCataDir) {
		t.Fatalf("ProjectCataRoot=%q", got)
	}
	if got := w.PersonaLocalPath(); got != filepath.Join(root, ProjectCataDir, RelPersonaLocal) {
		t.Fatalf("PersonaLocalPath=%q", got)
	}
	if got := w.ModeDir(ModeDefaultID); got != filepath.Join(root, ProjectCataDir, DirModes, ModeDefaultID) {
		t.Fatalf("ModeDir=%q", got)
	}
	if got := w.SkillDir("demo"); got != filepath.Join(root, ProjectCataDir, DirSkills, "demo") {
		t.Fatalf("SkillDir=%q", got)
	}
}

func TestResolveBrainDocAbs_projectFile(t *testing.T) {
	root := t.TempDir()
	w := &Workspace{ID: "ws", RootPath: root}
	abs, err := ResolveBrainDocAbs(w, "modes/_default/persona.md")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ProjectCataDir, "modes", "_default", "persona.md")
	if abs != want {
		t.Fatalf("abs=%q want %q", abs, want)
	}
}
