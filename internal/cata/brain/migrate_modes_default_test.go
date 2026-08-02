package brain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateModesDefaultV2_renamesOrchestrator(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	root := t.TempDir()
	ws := &Workspace{ID: "ws-mig", RootPath: root, ActiveMode: ModeAliasOrchestratorID}
	if err := os.MkdirAll(ws.Dir(), 0755); err != nil {
		t.Fatal(err)
	}
	orch := filepath.Join(ws.ProjectCataRoot(), DirModes, ModeAliasOrchestratorID)
	if err := os.MkdirAll(orch, 0755); err != nil {
		t.Fatal(err)
	}
	persona := "# Persona\n\nstock voice\n"
	if err := os.WriteFile(filepath.Join(orch, FilePersona), []byte(persona), 0644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(ws.ProjectCataRoot(), legacyMigratedModesOrchestratorV1), []byte("v1\n"), 0644)

	if err := DebugMigrateModesDefaultV2(ws); err != nil {
		t.Fatal(err)
	}
	defPersona := filepath.Join(ws.ModeDir(ModeDefaultID), FilePersona)
	data, err := os.ReadFile(defPersona)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != persona {
		t.Fatalf("persona not moved: %q", data)
	}
	if dirExists(orch) {
		t.Fatal("_orchestrator should be removed")
	}
	marker := filepath.Join(ws.ProjectCataRoot(), MigratedModesDefaultV2MarkerName())
	if _, err := os.Stat(marker); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws.ProjectCataRoot(), legacyMigratedModesOrchestratorV1)); !os.IsNotExist(err) {
		t.Fatal("legacy orchestrator marker should be removed")
	}
	got, err := loadMetaActiveMode(ws)
	if err != nil {
		t.Fatal(err)
	}
	if got != ModeDefaultID {
		t.Fatalf("active_mode=%q want %q", got, ModeDefaultID)
	}
	// idempotent
	if err := DebugMigrateModesDefaultV2(ws); err != nil {
		t.Fatal(err)
	}
}

func TestMaybeMigrateModesDefaultV2_keepsExistingDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	root := t.TempDir()
	ws := &Workspace{ID: "ws-en", RootPath: root, ActiveMode: ModeDefaultID}
	if err := os.MkdirAll(ws.Dir(), 0755); err != nil {
		t.Fatal(err)
	}
	def := filepath.Join(ws.ProjectCataRoot(), DirModes, ModeDefaultID)
	if err := os.MkdirAll(def, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(def, FilePersona), []byte("# Persona\n\nkeep me\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ws.maybeMigrateModesDefaultV2(); err != nil {
		t.Fatal(err)
	}
	if !dirExists(ws.ModeDir(ModeDefaultID)) {
		t.Fatal("expected _default")
	}
}
