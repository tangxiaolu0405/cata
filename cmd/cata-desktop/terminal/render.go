package terminal

import (
	"image/color"

	widget2 "cata/cmd/cata-desktop/terminal/internal/widget"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
)

const cursorWidth = 2

type render struct {
	term *Terminal
}

func (r *render) Layout(s fyne.Size) {
	r.term.content.Resize(s)
}

func (r *render) MinSize() fyne.Size {
	return fyne.NewSize(0, 0) // don't get propped open by the text cells
}

func (r *render) Refresh() {
	// Fyne 渲染刷新必须落在主线程：Render.Refresh() 由框架可能在任意线程触发
	// （PTY 输出 goroutine 经 fyne.DoAndWait 调度后仍保留这层保险），
	// 光标位置/样式的画布操作统一回主线程执行以免跨线程 panic。
	fyne.Do(func() {
		r.moveCursor()
		r.term.refreshCursor()
	})

	r.term.content.Refresh()
}

func (r *render) BackgroundColor() color.Color {
	return color.Transparent
}

func (r *render) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.term.content, r.term.cursor}
}

func (r *render) Destroy() {
}

func (r *render) moveCursor() {
	cell := r.term.guessCellSize()
	r.term.cursor.Move(fyne.NewPos(cell.Width*float32(r.term.cursorCol), cell.Height*float32(r.term.cursorRow)))
}

func (t *Terminal) refreshCursor() {
	t.cursor.Hidden = !t.focused || t.cursorHidden
	if t.bell.Load() {
		t.cursor.FillColor = theme.Color(theme.ColorNameError)
	} else {
		t.cursor.FillColor = theme.Color(theme.ColorNamePrimary)
	}
	t.cursor.Resize(fyne.NewSize(cursorWidth, t.guessCellSize().Height))
	t.cursor.Refresh()
}

// CreateRenderer requests a new renderer for this terminal (just a wrapper around the TextGrid)
func (t *Terminal) CreateRenderer() fyne.WidgetRenderer {
	t.ExtendBaseWidget(t)

	t.content = widget2.NewTermGrid()
	t.setupShortcuts()

	t.cursor = canvas.NewRectangle(theme.Color(theme.ColorNamePrimary))
	t.cursor.Hidden = true
	t.cursor.Resize(fyne.NewSize(cursorWidth, t.guessCellSize().Height))

	r := &render{term: t}
	t.cursorMoved = r.moveCursor
	return r
}
