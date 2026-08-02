package brain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestModesCatalogPromptBlock(t *testing.T) {
	ws, _ := testWorkspace(t, "modes-cat")
	if err := ws.EnsureScaffold(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ws.ModeDir("daban-fupan"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.ModeDir("daban-fupan"), FilePersona), []byte("# P\n\n我是复盘分身。\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(ws.ModeDir("daban-fupan"), ".draft"), []byte("x\n"), 0644)
	block := ModesCatalogPromptBlock(ws)
	if !strings.Contains(block, "daban-fupan") || !strings.Contains(block, "delegate_task") {
		t.Fatalf("catalog=%q", block)
	}
	if !strings.Contains(block, "[draft]") {
		t.Fatalf("expected draft mark: %q", block)
	}
}

func TestEnsureDefaultSpecialistRoute(t *testing.T) {
	ws, _ := testWorkspace(t, "route-ws")
	if err := ws.EnsureScaffold(); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDefaultSpecialistRoute(ws, "daban-fupan", "复盘专职"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(ws.ModeDir(ModeDefaultID), FileBehavior))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "## Specialist modes") || !strings.Contains(body, "mode_id=daban-fupan") {
		t.Fatalf("behavior=%q", body)
	}
	if err := EnsureDefaultSpecialistRoute(ws, "daban-fupan", "复盘专职更新"); err != nil {
		t.Fatal(err)
	}
	data2, _ := os.ReadFile(filepath.Join(ws.ModeDir(ModeDefaultID), FileBehavior))
	if c := strings.Count(string(data2), "mode_id=daban-fupan"); c != 1 {
		t.Fatalf("want 1 route line, got %d in %q", c, data2)
	}
}

func TestLongMemoryHotPromptBlock(t *testing.T) {
	ws, _ := testWorkspace(t, "hot-ws")
	long := ws.Path(RelMemoryLongLearnings)
	if err := os.MkdirAll(filepath.Dir(long), 0755); err != nil {
		t.Fatal(err)
	}
	body := "# Evolution learnings\n\n## 2026-07-27\n\n- 复盘须检查炸板率\n- 候选池最多10只\n"
	if err := os.WriteFile(long, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	hot := LongMemoryHotPromptBlock(ws, 1800)
	if !strings.Contains(hot, "炸板率") || !strings.Contains(hot, LongMemoryHotPrefix) {
		t.Fatalf("hot=%q", hot)
	}
}

func TestHasSpecialistModes(t *testing.T) {
	ws, _ := testWorkspace(t, "spec-ws")
	if err := ws.EnsureScaffold(); err != nil {
		t.Fatal(err)
	}
	if HasSpecialistModes(ws) {
		t.Fatal("only _default")
	}
	_ = os.MkdirAll(ws.ModeDir("x"), 0755)
	if !HasSpecialistModes(ws) {
		t.Fatal("expected specialist")
	}
}
