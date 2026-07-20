package brain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestQuarantineSkillRemovesCapability(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	root := t.TempDir()
	w := &Workspace{ID: "ws-sk", RootPath: root, ActiveMode: "default"}
	modeDir := w.ModeDir("default")
	skillDir := w.SkillDir("demo")
	if err := os.MkdirAll(modeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, FileSkillManifest), []byte("runner: node\nentry: main.js\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "main.js"), []byte("console.log(1)\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := AppendSkillToCapabilities(w, "demo"); err != nil {
		t.Fatal(err)
	}
	if err := QuarantineSkill(w, "demo", "boom", "out"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
		t.Fatalf("skill dir should move: %v", err)
	}
	caps := ParseCapabilitiesYAML(mustRead(t, w.CapabilitiesPath()))
	for _, s := range caps.Skills {
		if s == "demo" {
			t.Fatal("demo still in capabilities")
		}
	}
	failedRoot := filepath.Join(w.ProjectCataRoot(), DirSkills, ".failed")
	entries, err := os.ReadDir(failedRoot)
	if err != nil || len(entries) == 0 {
		t.Fatalf("failed dir: %v entries=%v", err, entries)
	}
}

func TestLoadSkillManifestRequiresRunner(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileSkillManifest), []byte("entry: main.js\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSkillManifest(dir); err == nil {
		t.Fatal("expected error without runner")
	}
}

func TestLoadSkillManifestVerifyEntry(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileSkillManifest), []byte("runner: node\nentry: main.js\nverify_entry: verify.js\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadSkillManifest(dir)
	if err != nil || m.VerifyEntry != "verify.js" || m.Runner != "node" {
		t.Fatalf("m=%+v err=%v", m, err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
