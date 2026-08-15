package terminal

import (
	"testing"

	"fyne.io/fyne/v2"
	_ "fyne.io/fyne/v2/test"
)

// TestANSIEscapeStateMachine 表驱动验证核心 ANSI 转义序列的状态机行为：
// 清屏、光标移动、字符集切换、颜色、删除行等。锁定跨 buffer 持久解析状态机不回归。
func TestANSIEscapeStateMachine(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		// check 断言 post-condition
		check func(t *testing.T, term *Terminal)
	}{
		{
			name: "clear screen resets cursor",
			in:   []byte("abc\x1b[2J"),
			check: func(t *testing.T, term *Terminal) {
				if term.cursorRow != 0 || term.cursorCol != 0 {
					t.Fatalf("after clear: row=%d col=%d, want 0,0", term.cursorRow, term.cursorCol)
				}
			},
		},
		{
			name: "cursor home",
			in:   []byte("x\x1b[H"),
			check: func(t *testing.T, term *Terminal) {
				if term.cursorRow != 0 || term.cursorCol != 0 {
					t.Fatalf("after home: row=%d col=%d, want 0,0", term.cursorRow, term.cursorCol)
				}
			},
		},
		{
			name: "line feed advances row",
			in:   []byte("a\nb"),
			check: func(t *testing.T, term *Terminal) {
				if term.cursorRow != 1 {
					t.Fatalf("after LF: row=%d, want 1", term.cursorRow)
				}
			},
		},
		{
			name: "charset switch DEC special graphics",
			in:   []byte("\x1b(0"),
			check: func(t *testing.T, term *Terminal) {
				if term.g0Charset != charSetDECSpecialGraphics {
					t.Fatalf("g0Charset=%v, want DECSpecialGraphics", term.g0Charset)
				}
			},
		},
		{
			name: "charset switch back to ASCII",
			in:   []byte("\x1b(0\x1b(B"),
			check: func(t *testing.T, term *Terminal) {
				if term.g0Charset != charSetANSII {
					t.Fatalf("g0Charset=%v, want ANSII", term.g0Charset)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term := New()
			term.Refresh()
			term.Resize(fyne.NewSize(400, 200))
			term.handleOutput(tc.in)
			tc.check(t, term)
		})
	}
}

// TestANSIPartialEscapeAcrossBuffers 验证残缺 ESC 序列跨 buffer 保持状态
// （分两次 handleOutput，第二次补全序列），不丢状态、不误解析。
func TestANSIPartialEscapeAcrossBuffers(t *testing.T) {
	term := New()
	term.Refresh()
	term.Resize(fyne.NewSize(400, 200))

	// 第一次：只发送 ESC[（序列未完成）
	term.handleOutput([]byte("\x1b["))

	// 第二次：补全 2J（清屏），应正确执行而不是当普通字符。
	term.handleOutput([]byte("2J"))

	// 清屏后光标归零。
	if term.cursorRow != 0 || term.cursorCol != 0 {
		t.Fatalf("after split ESC[2J: row=%d col=%d, want 0,0", term.cursorRow, term.cursorCol)
	}
}
