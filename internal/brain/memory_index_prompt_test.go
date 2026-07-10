package brain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cata/internal/config"
)

func TestMemoryIndexPromptBlockFor_fullSkipsProjectContent(t *testing.T) {
	ws, _ := testWorkspace(t, "idx-full")
	idxPath := ws.MemoryIndexPath()
	if err := os.MkdirAll(filepath.Dir(idxPath), 0755); err != nil {
		t.Fatal(err)
	}
	data := `{
  "version": 1,
  "entries": [
    {"id":"p1","source":"modes/_default/persona.md","summary":"Persona","category":"preference","priority":9},
    {"id":"p2","source":"modes/default/persona.md","summary":"Legacy","category":"preference","priority":9},
    {"id":"b1","source":"modes/_default/behavior.md","summary":"Behavior","category":"procedure","priority":6},
    {"id":"l1","source":"memory/long/learnings.md","summary":"Playbook","category":"procedure","priority":6},
    {"id":"pl","source":"persona.local.md","summary":"Focus","category":"fact","priority":7}
  ]
}`
	if err := os.WriteFile(idxPath, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	SetActive(ws)
	defer SetActive(nil)

	full := MemoryIndexPromptBlockFor(PromptProfileFull, 2800)
	if strings.Contains(full, "modes/_default/persona.md") || strings.Contains(full, "modes/_default/behavior.md") {
		t.Fatalf("full should skip modes in index: %q", full)
	}
	if strings.Contains(full, "persona.local.md —") {
		t.Fatalf("full should skip persona.local in index: %q", full)
	}
	if !strings.Contains(full, "memory/long/learnings.md") {
		t.Fatalf("full should keep long memory index: %q", full)
	}

	task := MemoryIndexPromptBlockFor(PromptProfileTask, 2800)
	if !strings.Contains(task, "modes/_default/persona.md") {
		t.Fatalf("task should keep persona in index: %q", task)
	}
}

func TestCanonicalizeIndexEntries_dedupesLegacyModePath(t *testing.T) {
	idx := &MemoryIndex{
		Entries: []IndexEntry{
			{Source: "modes/default/persona.md", Summary: "a"},
			{Source: "modes/_default/persona.md", Summary: "b"},
		},
	}
	canonicalizeIndexEntries(idx)
	if len(idx.Entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(idx.Entries))
	}
	if idx.Entries[0].Source != "modes/_default/persona.md" {
		t.Fatalf("got %q", idx.Entries[0].Source)
	}
}

func TestIsProjectContentFullInjectSource(t *testing.T) {
	if !isProjectContentFullInjectSource("modes/_default/persona.md") {
		t.Fatal("persona")
	}
	if isProjectContentFullInjectSource("memory/long/learnings.md") {
		t.Fatal("learnings not project content")
	}
}

func TestSaveMemoryIndex_canonicalizesOnWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)
	ws := &Workspace{ID: "save-ws", RootPath: home, ActiveMode: ModeDefaultID}
	if err := os.MkdirAll(ws.Dir(), 0755); err != nil {
		t.Fatal(err)
	}
	SetActive(ws)
	defer SetActive(nil)
	idx := &MemoryIndex{
		Entries: []IndexEntry{
			{Source: "modes/default/behavior.md", Summary: "x", Category: "procedure", Priority: 6},
			{Source: "modes/_default/behavior.md", Summary: "y", Category: "procedure", Priority: 6},
		},
	}
	if err := SaveMemoryIndexFor(ws, idx); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadMemoryIndexFor(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Entries) != 1 || loaded.Entries[0].Source != "modes/_default/behavior.md" {
		t.Fatalf("got %+v", loaded.Entries)
	}
}
