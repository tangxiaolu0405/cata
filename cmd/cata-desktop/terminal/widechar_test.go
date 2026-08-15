package terminal

import (
	"testing"

	"fyne.io/fyne/v2"
	_ "fyne.io/fyne/v2/test"
)

// TestWideCharNoOverlap 验证 CJK 宽字符占 2 列，并在其后预留空白续格：
// 宽字形不会溢出与下一个字符重叠（文字重叠问题）。
func TestWideCharNoOverlap(t *testing.T) {
	term := New()
	term.Refresh() // 初始化 content
	term.Resize(fyne.NewSize(400, 200))

	term.handleOutput([]byte("你好abc"))
	row := term.content.Rows[0]
	if len(row.Cells) != 7 {
		t.Fatalf("cells = %d, want 7（2宽+2宽+3窄）", len(row.Cells))
	}
	want := []rune{'你', 0, '好', 0, 'a', 'b', 'c'}
	for i, w := range want {
		if row.Cells[i].Rune != w {
			t.Fatalf("cell[%d] = %q, want %q", i, row.Cells[i].Rune, w)
		}
	}
	if term.cursorCol != 7 {
		t.Fatalf("cursorCol = %d, want 7", term.cursorCol)
	}
	if got := term.content.Text(); got != "你好abc" {
		t.Fatalf("Text() = %q, want %q", got, "你好abc")
	}
}

// TestWideCharBackspace 验证退格会跳过宽字符的续格，一次退到宽字符起点。
func TestWideCharBackspace(t *testing.T) {
	term := New()
	term.Refresh()
	term.Resize(fyne.NewSize(400, 200))

	term.handleOutput([]byte("你x"))
	if term.cursorCol != 3 {
		t.Fatalf("cursorCol = %d, want 3", term.cursorCol)
	}
	term.handleOutput([]byte{asciiBackspace}) // 退到 x 前
	if term.cursorCol != 2 {
		t.Fatalf("cursorCol after bs = %d, want 2", term.cursorCol)
	}
	term.handleOutput([]byte{asciiBackspace}) // 越过续格，回到宽字符起点
	if term.cursorCol != 0 {
		t.Fatalf("cursorCol after bs2 = %d, want 0", term.cursorCol)
	}
}

// TestWideCharLastCol 验证宽字符放不下时回退到单列，不越界。
func TestWideCharAtLastCol(t *testing.T) {
	term := New()
	term.Refresh()
	term.Resize(fyne.NewSize(400, 200))

	cols := int(term.config.Columns)
	term.moveCursor(0, cols-1)
	term.handleOutput([]byte("你"))
	if term.cursorCol != cols {
		t.Fatalf("cursorCol = %d, want %d（触发下一字符换行）", term.cursorCol, cols)
	}
}

// TestWideCharSelectedText 验证选中复制时不会把续格（Rune 0/NUL）带进剪贴板文本。
func TestWideCharSelectedText(t *testing.T) {
	term := New()
	term.Refresh()
	term.Resize(fyne.NewSize(400, 200))

	term.handleOutput([]byte("你好abc"))
	// 选中整行（getSelectedRange 内部转 0-based）
	term.selStart = &position{Col: 1, Row: 1}
	term.selEnd = &position{Col: 8, Row: 1}
	if got := term.SelectedText(); got != "你好abc" {
		t.Fatalf("SelectedText() = %q, want %q", got, "你好abc")
	}
}
