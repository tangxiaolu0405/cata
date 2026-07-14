package brain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cata/internal/cata/config"
)

func testWorkspace(t *testing.T, id string) (*Workspace, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)
	ws := &Workspace{
		ID:         id,
		RootPath:   filepath.Join(home, "project"),
		ActiveMode: ModeDefaultID,
	}
	if err := os.MkdirAll(ws.Dir(), 0755); err != nil {
		t.Fatal(err)
	}
	return ws, home
}

func TestMigrateLearningFragments_mergesAndArchives(t *testing.T) {
	ws, _ := testWorkspace(t, "test-ws")
	fragDir := filepath.Join(ws.LongTermDir(), "learnings")
	if err := os.MkdirAll(fragDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fragDir, "learning-20260701-120000.md"),
		[]byte("# Evolution learning\n\n用户要求按板块强度排序取涨停推荐列表\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fragDir, "learning-20260702-130000.md"),
		[]byte("# Evolution learning\n\n子 agent 路径须与 Windows output_cwd 一致\n"), 0644); err != nil {
		t.Fatal(err)
	}
	idxPath := ws.MemoryIndexPath()
	if err := os.MkdirAll(filepath.Dir(idxPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(idxPath, []byte(`{
  "version": 1,
  "entries": [
    {"id":"learning-old","source":"memory/long/learnings/learning-20260701-120000.md","summary":"x","category":"fact","priority":5}
  ]
}`), 0644); err != nil {
		t.Fatal(err)
	}

	SetActive(ws)
	defer SetActive(nil)
	if err := MigrateLearningFragmentsFor(ws); err != nil {
		t.Fatal(err)
	}

	playbook, err := os.ReadFile(ws.Path(RelMemoryLongLearnings))
	if err != nil {
		t.Fatalf("playbook: %v", err)
	}
	body := string(playbook)
	if !strings.Contains(body, "板块强度") || !strings.Contains(body, "output_cwd") {
		t.Fatalf("missing merged content: %q", body)
	}
	if _, err := os.Stat(filepath.Join(fragDir, "learning-20260701-120000.md")); !os.IsNotExist(err) {
		t.Fatalf("fragment should be archived, stat err=%v", err)
	}
	idx, err := LoadMemoryIndexFor(ws)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range idx.Entries {
		if strings.Contains(e.Source, "learnings/learning-") {
			t.Fatalf("legacy index entry remains: %+v", e)
		}
	}
	foundPlaybook := false
	for _, e := range idx.Entries {
		if e.Source == RelMemoryLongLearnings {
			foundPlaybook = true
		}
	}
	if !foundPlaybook {
		t.Fatal("playbook not indexed")
	}
	if _, err := os.Stat(ws.Path(filepath.Join("memory", fileLearningPlaybookMigrate))); err != nil {
		t.Fatalf("marker missing: %v", err)
	}
	if err := MigrateLearningFragmentsFor(ws); err != nil {
		t.Fatal(err)
	}
}

func TestSyncMemoryIndexAfterEvolution_playbookNotFragments(t *testing.T) {
	ws, _ := testWorkspace(t, "ws2")
	if err := os.MkdirAll(ws.LongTermDir(), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ws.MemoryIndexPath(), []byte(`{"version":1,"entries":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	SetActive(ws)
	defer SetActive(nil)

	learn := strings.Repeat("x", 30)
	if err := SyncMemoryIndexAfterEvolution(nil, learn, ""); err != nil {
		t.Fatal(err)
	}
	frag := filepath.Join(ws.LongTermDir(), "learnings")
	if entries, err := os.ReadDir(frag); err == nil && len(entries) > 0 {
		t.Fatalf("should not create learnings/ fragments, got %d files", len(entries))
	}
	data, err := os.ReadFile(ws.Path(RelMemoryLongLearnings))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), learn) {
		t.Fatalf("playbook missing learning: %q", string(data))
	}
}

func TestInferIndexCategory_learningsPlaybook(t *testing.T) {
	cat, pri, _ := inferIndexCategory(RelMemoryLongLearnings)
	if cat != "procedure" || pri != 6 {
		t.Fatalf("got %s p%d", cat, pri)
	}
}
