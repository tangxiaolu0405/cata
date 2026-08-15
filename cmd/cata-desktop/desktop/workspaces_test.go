package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

// 临时隔离：CATA_HOME 指向测试目录，避免碰真实 ~/.cata。
func testCataHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), ".cata")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CATA_HOME", home)
	return home
}

func TestAddRemoveExtraWorkspace(t *testing.T) {
	testCataHome(t)
	a := &App{}

	root := t.TempDir()
	if err := a.AddWorkspace(root); err != nil {
		t.Fatalf("AddWorkspace: %v", err)
	}
	if err := a.AddWorkspace(root); err != nil { // 重复添加应幂等
		t.Fatalf("AddWorkspace dup: %v", err)
	}
	list, err := a.ListWorkspaces()
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(list) != 1 || list[0].RootPath != root || list[0].Linked {
		t.Fatalf("unexpected workspaces: %+v", list)
	}
	if err := a.RemoveWorkspace(root); err != nil {
		t.Fatalf("RemoveWorkspace: %v", err)
	}
	list, _ = a.ListWorkspaces()
	if len(list) != 0 {
		t.Fatalf("expected empty after remove, got %+v", list)
	}
	if list == nil {
		t.Fatal("expected non-nil empty slice (Wails serializes nil to JS null)")
	}
}

func TestAddWorkspaceRejectsFile(t *testing.T) {
	testCataHome(t)
	a := &App{}
	f := filepath.Join(t.TempDir(), "x.txt")
	if err := os.WriteFile(f, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.AddWorkspace(f); err == nil {
		t.Fatal("expected error for non-directory")
	}
}

func TestListDirSortsDirsFirst(t *testing.T) {
	a := &App{}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "b-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a-file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "A-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := a.ListDir(root)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	// 目录在前（A-dir < b-dir 按名），文件在后
	want := []string{"A-dir", "b-dir", "a-file.txt"}
	for i, w := range want {
		if entries[i].Name != w {
			t.Fatalf("order[%d] = %s, want %s", i, entries[i].Name, w)
		}
	}
}

func TestReadFileBinaryDetection(t *testing.T) {
	a := &App{}
	dir := t.TempDir()
	bin := filepath.Join(dir, "a.bin")
	if err := os.WriteFile(bin, []byte{0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}
	fc, err := a.ReadFile(bin)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !fc.Binary {
		t.Fatal("expected binary=true")
	}
	txt := filepath.Join(dir, "b.md")
	if err := os.WriteFile(txt, []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fc, err = a.ReadFile(txt)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if fc.Binary || fc.Content != "# hi\n" {
		t.Fatalf("unexpected text result: %+v", fc)
	}
}
