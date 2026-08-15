package desktop

import (
	"testing"

	"fyne.io/fyne/v2"
)

// hugeObject 模拟一个 MinSize 极大的子内容（超长文件行 / 超长路径标题），
// 用于回归验证布局 MinSize 不会被内容撑大。
type hugeObject struct {
	visible bool
}

func (h hugeObject) MinSize() fyne.Size      { return fyne.NewSize(5000, 5000) }
func (h hugeObject) Move(fyne.Position)      {}
func (h hugeObject) Resize(fyne.Size)        {}
func (h hugeObject) Position() fyne.Position { return fyne.Position{} }
func (h hugeObject) Size() fyne.Size         { return fyne.Size{} }
func (h hugeObject) Visible() bool           { return h.visible }
func (h hugeObject) Show()                   {}
func (h hugeObject) Hide()                   {}
func (h hugeObject) Refresh()                {}

func TestFixedSplitLayoutMinSizeBounded(t *testing.T) {
	l := &fixedSplitLayout{leftWidth: 300}
	objs := []fyne.CanvasObject{hugeObject{true}, hugeObject{true}, hugeObject{true}}
	got := l.MinSize(objs)
	want := fyne.NewSize(300+1+titleBarWidth, 240)
	if got != want {
		t.Fatalf("MinSize = %v, want %v（不能被子内容撑大）", got, want)
	}
}

// hbarMock 实现 hbarLike，记录 setView/sync 调用，验证 hbarOverlayLayout
// 把滚动条放到标题栏正下方（Y = titleBarHeight）并横跨整个平铺区。
type hbarMock struct {
	trackingObject
	view float32
	seen int
}

func (m *hbarMock) setView(v float32) { m.view = v }
func (m *hbarMock) sync()             { m.seen++ }

func TestHBarOverlayLayoutPositionsBelowTitle(t *testing.T) {
	l := &hbarOverlayLayout{}
	bar := &hbarMock{}
	objs := []fyne.CanvasObject{bar}

	l.Layout(objs, fyne.NewSize(900, 600))
	if bar.view != 900 {
		t.Fatalf("setView = %v, want 900（视口宽 = 平铺区宽）", bar.view)
	}
	if bar.pos.X != 0 || bar.pos.Y != titleBarHeight {
		t.Fatalf("滚动条位置 = (%v,%v), want (0,%v)（标题栏正下方）", bar.pos.X, bar.pos.Y, titleBarHeight)
	}
	if bar.size.Width != 900 || bar.size.Height != scrollbarRowH {
		t.Fatalf("滚动条尺寸 = %v, want 900x%d（横跨平铺区）", bar.size, scrollbarRowH)
	}
	if bar.seen == 0 {
		t.Fatal("Layout 应调用 sync 刷新滚动条")
	}
	if m := l.MinSize(objs); m != fyne.NewSize(1, scrollbarRowH) {
		t.Fatalf("MinSize = %v, want 1x%d", m, scrollbarRowH)
	}
}

// TestTileLayoutMinSizeNotFromContent 验证：MinSize 宽度 = 可见窗口数 ×
// minWidth（固定值），不随子内容（超长文件/超长标题）变化。
func TestTileLayoutMinSizeNotFromContent(t *testing.T) {
	l := &tileLayout{minWidth: 320}
	// 注意：对象列表最后一个固定是 empty 占位，不参与窗口计数。
	objs := []fyne.CanvasObject{
		hugeObject{true}, hugeObject{true}, hugeObject{true}, hugeObject{false},
	}
	got := l.MinSize(objs)
	want := fyne.NewSize(3*320, 240)
	if got != want {
		t.Fatalf("MinSize = %v, want %v（由窗口数决定，不由子内容决定）", got, want)
	}
	// 全部隐藏：只剩 empty，最小 320 宽
	hidden := []fyne.CanvasObject{
		hugeObject{false}, hugeObject{false}, hugeObject{false}, hugeObject{false},
	}
	if m := l.MinSize(hidden); m != fyne.NewSize(320, 240) {
		t.Fatalf("empty MinSize = %v, want 320x240", m)
	}
}

// TestTileLayoutScrollingWhenOverflow 验证：窗口总宽超过可用宽度时，
// 每个窗口保持 minW 定宽（不继续压缩），超出部分由外层 HScroll 滚动。
func TestTileLayoutScrollingWhenOverflow(t *testing.T) {
	l := &tileLayout{minWidth: 320}
	var objs []fyne.CanvasObject
	for i := 0; i < 4; i++ {
		objs = append(objs, &trackingObject{visible: true})
	}
	objs = append(objs, &trackingObject{visible: false}) // empty

	// 可用宽度 1000，4 个窗口需要 1280：每个窗口保持 320，不压到 250
	l.Layout(objs, fyne.NewSize(1000, 500))
	for i, o := range objs[:4] {
		to := o.(*trackingObject)
		if to.size.Width != 320 {
			t.Fatalf("window %d width = %v, want 320（保持最小宽度）", i, to.size.Width)
		}
		if to.pos.X != float32(i)*320 {
			t.Fatalf("window %d pos.X = %v, want %v", i, to.pos.X, float32(i)*320)
		}
	}
	// 内容最小宽度 = 1280 > 1000，外层 HScroll 出现横向滚动
	if m := l.MinSize(objs); m.Width != 4*320 {
		t.Fatalf("MinSize width = %v, want %v（内容超宽触发滚动）", m.Width, 4*320)
	}
}

// TestTileLayoutTracksVisiblePanels 验证：平铺布局只统计可见面板，
// 关闭一个后其余自动重排占满（regression：之前隐藏窗口后剩余不占满）。
func TestTileLayoutTracksVisiblePanels(t *testing.T) {
	l := &tileLayout{}
	p1 := &trackingObject{visible: true}
	p2 := &trackingObject{visible: true}
	empty := &trackingObject{visible: false}
	objs := []fyne.CanvasObject{p1, p2, empty}

	l.Layout(objs, fyne.NewSize(1000, 500))
	// 两个可见：等宽 500
	if p1.size.Width != 500 || p2.size.Width != 500 {
		t.Fatalf("两个窗口应等宽 500，got p1=%v p2=%v", p1.size, p2.size)
	}
	if empty.visible {
		t.Fatal("有窗口时 empty 不应可见")
	}

	// 关闭 p1（设为不可见后重新布局）：p2 应占满 1000
	p1.visible = false
	p2.visible = true
	l.Layout(objs, fyne.NewSize(1000, 500))
	if p2.size.Width != 1000 {
		t.Fatalf("只剩一个窗口时应占满 1000，got %v", p2.size.Width)
	}

	// 全部关闭：empty 应占满
	p2.visible = false
	l.Layout(objs, fyne.NewSize(1000, 500))
	if !empty.visible {
		t.Fatal("全部关闭后 empty 应显示")
	}
	if empty.size.Width != 1000 || empty.size.Height != 500 {
		t.Fatalf("empty 应占满，got %v", empty.size)
	}
}

type trackingObject struct {
	visible bool
	size    fyne.Size
	pos     fyne.Position
}

func (o *trackingObject) MinSize() fyne.Size { return fyne.NewSize(0, 0) }
func (o *trackingObject) Move(p fyne.Position) {
	o.pos = p
}
func (o *trackingObject) Resize(s fyne.Size) {
	o.size = s
}
func (o *trackingObject) Position() fyne.Position { return o.pos }
func (o *trackingObject) Size() fyne.Size         { return o.size }
func (o *trackingObject) Visible() bool           { return o.visible }
func (o *trackingObject) Show()                   { o.visible = true }
func (o *trackingObject) Hide()                   { o.visible = false }
func (o *trackingObject) Refresh()                {}
