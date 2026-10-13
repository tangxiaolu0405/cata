package brain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cata/internal/cata/config"
)

// TestSyncMemoryIndex_ExplicitWorkspaceNoGlobalLeak 回归：SyncMemoryIndexAfterEvolution
// 必须只读写传入的 workspace，不得因全局 Active() 指向别的 workspace 而串格。
// 历史 bug：indexEntryFromFile 内部用 Active()、Sync 内部用 LoadMemoryIndex()/SaveMemoryIndex()，
// 多 cata 并行（后台 evolve 会改写全局 Active）时索引会落到错误的脑子格。
func TestSyncMemoryIndex_ExplicitWorkspaceNoGlobalLeak(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)

	mk := func(id string) *Workspace {
		ws := &Workspace{ID: id, RootPath: filepath.Join(home, id), ActiveMode: ModeDefaultID}
		if err := os.MkdirAll(ws.Dir(), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(ws.MemoryIndexPath()), 0755); err != nil {
			t.Fatal(err)
		}
		return ws
	}
	wsA := mk("ws-a")
	wsB := mk("ws-b")

	// A 有一份 long 记忆文档；B 没有。
	docRel := "memory/long/topic-a.md"
	if err := os.MkdirAll(filepath.Dir(wsA.Path(docRel)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wsA.Path(docRel), []byte("# Topic A\n\nA 的专属事实\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// 关键：把全局 Active 指向 B，然后对 A 调 Sync。
	// 修复前 indexEntryFromFile 会去 B 找 topic-a.md（找不到）→ A 的索引为空；
	// 且 Load/Save 会落到 B 的 index.json。
	SetActive(wsB)
	defer SetActive(nil)

	if err := SyncMemoryIndexAfterEvolution(wsA, []string{docRel}, "", ""); err != nil {
		t.Fatalf("sync: %v", err)
	}

	idxA, err := LoadMemoryIndexFor(wsA)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range idxA.Entries {
		if e.Source == docRel {
			found = true
		}
	}
	if !found {
		t.Fatalf("wsA index missing %s (leaked to global Active?): %+v", docRel, idxA.Entries)
	}

	// B 的索引必须保持干净（没被 A 的同步污染）。
	idxB, err := LoadMemoryIndexFor(wsB)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range idxB.Entries {
		if e.Source == docRel {
			t.Fatalf("wsB index polluted by wsA sync: %+v", idxB.Entries)
		}
	}

	// 磁盘上 A 的 index.json 必须存在且含该条目。
	data, err := os.ReadFile(wsA.MemoryIndexPath())
	if err != nil {
		t.Fatalf("wsA index.json not written: %v", err)
	}
	if !strings.Contains(string(data), docRel) {
		t.Fatalf("wsA index.json missing entry: %s", string(data))
	}
}
