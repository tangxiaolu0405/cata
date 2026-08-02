package brain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveScaffoldDemoModes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	root := t.TempDir()
	ws := &Workspace{ID: "demo-clean", RootPath: root, ActiveMode: ModeDefaultID}
	if err := os.MkdirAll(ws.Dir(), 0755); err != nil {
		t.Fatal(err)
	}
	// plant demo seeds as if old scaffold did
	for id, persona := range scaffoldDemoPersonaByID {
		dir := ws.ModeDir(id)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, FilePersona), []byte(persona), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := ws.maybeRemoveScaffoldDemoModesV1(); err != nil {
		t.Fatal(err)
	}
	for _, id := range scaffoldDemoModeIDs {
		if dirExists(ws.ModeDir(id)) {
			t.Fatalf("demo mode %s should be removed", id)
		}
	}
	// idempotent
	if err := ws.maybeRemoveScaffoldDemoModesV1(); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveScaffoldDemoModes_keepsEdited(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	root := t.TempDir()
	ws := &Workspace{ID: "demo-keep", RootPath: root, ActiveMode: ModeDefaultID}
	dir := ws.ModeDir("coder")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	custom := scaffoldDemoPersonaByID["coder"] + "\n\n## Project-specific\n\nstock fupan rules live here and must not be wiped.\n"
	if err := os.WriteFile(filepath.Join(dir, FilePersona), []byte(custom), 0644); err != nil {
		t.Fatal(err)
	}
	orchStub := filepath.Join(ws.ProjectCataRoot(), DirModes, ModeAliasOrchestratorID)
	if err := os.MkdirAll(orchStub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orchStub, FilePersona), []byte("# Migrated\n\nThis mode directory is a stub. Use `modes/_default/`.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ws.maybeRemoveScaffoldDemoModesV1(); err != nil {
		t.Fatal(err)
	}
	if !dirExists(dir) {
		t.Fatal("edited coder mode must be kept")
	}
	if dirExists(orchStub) {
		t.Fatal("legacy _orchestrator stub should be removed")
	}
}
