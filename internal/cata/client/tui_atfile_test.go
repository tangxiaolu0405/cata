package client

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
)

// TestCurrentAtQuery 覆盖 @token 识别：行首/空白后触发，邮箱等非 token 不触发，
// 含空白说明 token 结束。
func TestCurrentAtQuery(t *testing.T) {
	cases := []struct {
		in    string
		token string
		ok    bool
	}{
		{"@", "", true},
		{"@ma", "ma", true},
		{"看图 @a.png", "a.png", true},
		{"看图@a.png", "", false},    // @ 前非空白
		{"email@x.com", "", false}, // 邮箱不触发
		{"@a.png 后面", "", false},   // token 后含空白
		{"第一行\n@b.go", "b.go", true},
		{"无 at", "", false},
	}
	for _, c := range cases {
		got := currentAtQuery(c.in)
		if got.ok != c.ok || got.token != c.token {
			t.Fatalf("currentAtQuery(%q)=%+v want {token:%q ok:%v}", c.in, got, c.token, c.ok)
		}
	}
}

// TestApplyAtCompletion 补全把当前 @token 替换为完整路径并补尾随空格，保留前缀文本。
func TestApplyAtCompletion(t *testing.T) {
	m := &model{input: newChatTextarea(), vp: viewport.New(80, 20), sidebarVP: newSidebarViewport()}
	m.input.SetValue("看图 @a.p")
	m.applyAtCompletion("dir/a.png")
	if got := m.input.Value(); got != "看图 @dir/a.png " {
		t.Fatalf("completion=%q", got)
	}
}

// TestMatchFiles 子串匹配 + 排序：文件名前缀命中优先，其次路径前缀，再次包含。
func TestMatchFiles(t *testing.T) {
	files := []string{"src/main.go", "main_test.go", "docs/main.md", "other.txt"}
	got := matchFiles(files, "main", 10)
	// 三者文件名均以 main 开头（rank 0），同级按路径字典序。
	want := []string{"docs/main.md", "main_test.go", "src/main.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matchFiles=%v want %v", got, want)
	}
	// 文件名前缀命中排在仅路径包含命中之前。
	got2 := matchFiles([]string{"x/foo.txt", "foo.go"}, "foo", 10)
	if !reflect.DeepEqual(got2, []string{"foo.go", "x/foo.txt"}) {
		t.Fatalf("rank order=%v", got2)
	}
	// 空查询返回全部（受 limit）。
	if got := matchFiles(files, "", 2); len(got) != 2 {
		t.Fatalf("empty query limit=%v", got)
	}
	// 无命中。
	if got := matchFiles(files, "zzz", 10); len(got) != 0 {
		t.Fatalf("no-match=%v", got)
	}
}

// TestFileIndexScan 扫描临时目录：跳过忽略目录、点文件、二进制扩展名与大文件。
func TestFileIndexScan(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel string, data []byte) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("main.go", []byte("package main"))
	mustWrite("src/app.go", []byte("package src"))
	mustWrite("docs/readme.md", []byte("hi"))
	mustWrite("node_modules/pkg/index.js", []byte("x"))     // 忽略目录
	mustWrite(".git/config", []byte("x"))                   // 忽略目录
	mustWrite(".hidden.go", []byte("x"))                    // 点文件
	mustWrite("bin/app.exe", []byte("x"))                   // 二进制扩展名
	mustWrite("big.txt", make([]byte, maxIndexFileBytes+1)) // 超大文件

	fi := newFileIndex(root)
	got := fi.Files()
	want := []string{"docs/readme.md", "main.go", "src/app.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scan=%v want %v", got, want)
	}
}

// TestAtMatchesAndSync 索引候选驱动 atList：输入 @ 后菜单打开，含匹配项。
func TestAtMatchesAndSync(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "alpha.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "beta.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &model{
		input:     newChatTextarea(),
		vp:        viewport.New(80, 20),
		sidebarVP: newSidebarViewport(),
		fileIdx:   newFileIndex(root),
		width:     100,
		height:    30,
	}
	m.input.SetValue("@al")
	got := m.atMatches()
	if len(got) != 1 || got[0] != "alpha.go" {
		t.Fatalf("atMatches=%v", got)
	}
	m.syncAtList()
	if m.atList == nil {
		t.Fatal("atList should be built for @al")
	}
	// 无 @ 时菜单关闭。
	m.input.SetValue("普通文本")
	m.syncAtList()
	if m.atList != nil {
		t.Fatal("atList should be nil without @token")
	}
}

// TestAtTokenComplete 完整路径命中时 Enter 应放行（不重复补全）。
func TestAtTokenComplete(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &model{input: newChatTextarea(), vp: viewport.New(80, 20), sidebarVP: newSidebarViewport(), fileIdx: newFileIndex(root)}
	m.input.SetValue("@main.go")
	if !m.atTokenComplete() {
		t.Fatal("exact path should be complete")
	}
	m.input.SetValue("@mai")
	if m.atTokenComplete() {
		t.Fatal("partial path should not be complete")
	}
}

// TestAtCompletionFeedsAttachment 端到端：@ 补全产出的 `@<path>` 能被
// splitAttachmentTokens 识别为附件（补全与发送链路一致）。
func TestAtCompletionFeedsAttachment(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "readme.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &model{input: newChatTextarea(), vp: viewport.New(80, 20), sidebarVP: newSidebarViewport(), fileIdx: newFileIndex(root)}
	m.input.SetValue("总结 @read")
	m.applyAtCompletion("docs/readme.md")
	txt, paths := splitAttachmentTokens(m.input.Value())
	if txt != "总结" || len(paths) != 1 || paths[0] != "docs/readme.md" {
		t.Fatalf("line=%q txt=%q paths=%v", m.input.Value(), txt, paths)
	}
}

// TestAtMenuLines 菜单行数随候选数变化，且无菜单时为 0。
func TestAtMenuLines(t *testing.T) {
	m := &model{input: newChatTextarea(), vp: viewport.New(80, 20), sidebarVP: newSidebarViewport(), fileIdx: newFileIndex(t.TempDir()), width: 100, height: 30}
	if m.atMenuLines() != 0 {
		t.Fatal("no menu should be 0 lines")
	}
	root := m.fileIdx.root
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.input.SetValue("@")
	m.syncAtList()
	if m.atMenuLines() < 2 {
		t.Fatalf("menu lines=%d want >=2", m.atMenuLines())
	}
}
