package brain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cata/internal/cata/config"
)

func TestListHomeWorkspaces(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)

	wsRoot := filepath.Join(home, "brain", "workspaces")
	proj := filepath.Join(home, "proj-a")
	if err := os.MkdirAll(filepath.Join(wsRoot, "ws-a"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(proj, 0755); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(map[string]string{
		"id":        "ws-a",
		"root_path": proj,
		"kind":      "ephemeral",
		"name":      "Alpha",
	})
	if err := os.WriteFile(filepath.Join(wsRoot, "ws-a", "meta.json"), meta, 0644); err != nil {
		t.Fatal(err)
	}

	workerProj := filepath.Join(home, ".cata_worker", "telegram", "1")
	_ = os.MkdirAll(workerProj, 0755)
	_ = os.MkdirAll(filepath.Join(wsRoot, "worker-cell"), 0755)
	wmeta, _ := json.Marshal(map[string]string{
		"id":        "worker-cell",
		"root_path": workerProj,
		"kind":      "ephemeral",
	})
	_ = os.WriteFile(filepath.Join(wsRoot, "worker-cell", "meta.json"), wmeta, 0644)

	got, err := ListHomeWorkspaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 workspace, got %+v", got)
	}
	if got[0].ID != "ws-a" || got[0].Name != "Alpha" {
		t.Fatalf("got %+v", got[0])
	}
	if _, ok := FindHomeWorkspace("ws-a"); !ok {
		t.Fatal("FindHomeWorkspace missed")
	}
	if _, ok := FindHomeWorkspace("worker-cell"); ok {
		t.Fatal("worker cell should be filtered")
	}
}
