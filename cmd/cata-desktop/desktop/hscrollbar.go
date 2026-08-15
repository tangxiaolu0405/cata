package desktop

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// scrollbarRowH 水平滚动条行高：位于窗口标题栏正下方、内容区之上，
// 叠放于每个面板在标题下预留的空隙处（见 panel.go 的 gapWrap）。
const scrollbarRowH = 12

// minThumb 水平滚动条滑块的最小宽度。
const minThumb = 32

// hiddenScrollTheme 只把滚动条隐藏（颜色透明 + 尺寸 0），其余外观沿用
// cataTheme。用于包住平铺区 HScroll：Fyne 内置滚动条固定在视口底部、
// 无法移到标题下方，因此这里把它彻底隐藏，由 hscrollBar 自绘在标题下方。
type hiddenScrollTheme struct {
	fyne.Theme
}

func (t *hiddenScrollTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameScrollBar, theme.ColorNameScrollBarBackground:
		return color.Transparent
	}
	return t.Theme.Color(name, variant)
}

func (t *hiddenScrollTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameScrollBar, theme.SizeNameScrollBarSmall:
		return 0
	}
	return t.Theme.Size(name)
}

// newHiddenScrollTheme 构造「仅隐藏滚动条」的主题（基于 cataTheme）。
func newHiddenScrollTheme() fyne.Theme {
	return &hiddenScrollTheme{Theme: newCataTheme()}
}

// hscrollBar 自绘水平滚动条：细轨道 + 滑块，可拖动、可滚轮，宽度随视口，
// 位置由 hbarOverlayLayout 放在窗口标题行正下方。它只是「镜子」——真正的
// 滚动由外层 HScroll 完成（触控板/滚轮），hscrollBar 与 HScroll 通过 offset
// 双向同步：
//   - HScroll 滚动 → OnScrolled → setOffset（只更新滑块，不回调）
//   - 拖动/滚轮 → dragTo → onScroll → ScrollToOffset（真正滚动）
type hscrollBar struct {
	widget.BaseWidget

	track *canvas.Rectangle // 轨道
	thumb *canvas.Rectangle // 滑块

	content float32 // 内容总宽（可见窗口数 × minW）
	view    float32 // 视口宽（右侧平铺区宽）
	offset  float32 // 当前横向偏移

	onScroll func(offset float32) // 用户拖动/滚轮时回调（驱动 HScroll）

	dragging       bool
	dragStartThumb float32
	dragStartOff   float32
	draggedDX      float32
}

func newHScrollBar(onScroll func(offset float32)) *hscrollBar {
	b := &hscrollBar{onScroll: onScroll}
	b.ExtendBaseWidget(b)
	return b
}

// hbarLike 是 hbarOverlayLayout 依赖的最小接口（便于单测）。
type hbarLike interface {
	fyne.CanvasObject
	setView(v float32)
	sync()
}

var (
	_ fyne.Draggable  = (*hscrollBar)(nil)
	_ fyne.Scrollable = (*hscrollBar)(nil)
	_ hbarLike        = (*hscrollBar)(nil)
)

func (b *hscrollBar) CreateRenderer() fyne.WidgetRenderer {
	th := theme.CurrentForWidget(b)
	variant := fyne.CurrentApp().Settings().ThemeVariant()
	b.track = canvas.NewRectangle(th.Color(theme.ColorNameSeparator, variant))
	b.thumb = canvas.NewRectangle(th.Color(theme.ColorNamePrimary, variant))
	b.thumb.CornerRadius = 2
	return &hscrollBarRenderer{objects: []fyne.CanvasObject{b.track, b.thumb}, bar: b}
}

// setContent 设置内容总宽（面板数量变化后由 updateHScrollBar 调用）。
func (b *hscrollBar) setContent(w float32) {
	b.content = w
	b.sync()
}

// setView 设置视口宽（窗口尺寸变化时由 hbarOverlayLayout 调用）。
func (b *hscrollBar) setView(v float32) {
	b.view = v
}

// setOffset 只同步滑块位置（外部 HScroll 已滚动，来源 OnScrolled），不回调。
func (b *hscrollBar) setOffset(off float32) {
	b.offset = clampOffset(off, b.content, b.view)
	b.sync()
}

// dragTo 用户拖动/滚轮：夹紧偏移并回调外部真正滚动。
func (b *hscrollBar) dragTo(off float32) {
	off = clampOffset(off, b.content, b.view)
	if b.offset == off {
		return
	}
	b.offset = off
	if b.onScroll != nil {
		b.onScroll(off)
	}
	b.sync()
}

func clampOffset(off, content, view float32) float32 {
	if off < 0 {
		off = 0
	}
	if max := content - view; off > max {
		off = max
	}
	return off
}

// sync 依据内容/视口决定显示隐藏，并刷新滑块几何。
func (b *hscrollBar) sync() {
	if b.view > 0 && b.content > b.view {
		if !b.Visible() {
			b.Show()
		}
	} else if b.Visible() {
		b.Hide()
	}
	b.Refresh()
}

func (b *hscrollBar) Dragged(e *fyne.DragEvent) {
	if !b.dragging {
		b.dragging = true
		b.dragStartThumb = b.thumb.Position().X
		b.dragStartOff = b.offset
		b.draggedDX = 0
	}
	b.draggedDX += e.Dragged.DX

	tw := b.thumb.Size().Width
	trackW := b.Size().Width
	maxOff := b.content - b.view
	if trackW <= tw || maxOff <= 0 {
		return
	}
	maxThumb := trackW - tw

	newThumb := b.dragStartThumb + b.draggedDX
	if newThumb < 0 {
		newThumb = 0
	} else if newThumb > maxThumb {
		newThumb = maxThumb
	}
	off := b.dragStartOff + (newThumb-b.dragStartThumb)/maxThumb*maxOff
	b.dragTo(off)
}

func (b *hscrollBar) DragEnd() {
	b.dragging = false
}

func (b *hscrollBar) Scrolled(ev *fyne.ScrollEvent) {
	dx := ev.Scrolled.DX
	if dx == 0 {
		dx = ev.Scrolled.DY // 横向滚动条上也接受纵向滑动手势
	}
	b.dragTo(b.offset + dx)
}

// hscrollBarRenderer 渲染轨道 + 滑块。
type hscrollBarRenderer struct {
	objects []fyne.CanvasObject
	bar     *hscrollBar
}

func (r *hscrollBarRenderer) Destroy() {}

func (r *hscrollBarRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func (r *hscrollBarRenderer) Layout(size fyne.Size) {
	b := r.bar
	const barH = 4
	y := size.Height - barH

	b.track.Move(fyne.NewPos(0, y))
	b.track.Resize(fyne.NewSize(size.Width, barH))

	if b.content <= b.view || b.view <= 0 {
		b.thumb.Hide()
		return
	}
	b.thumb.Show()

	tw := size.Width * (b.view / b.content)
	if tw < minThumb {
		tw = minThumb
	}
	if tw > size.Width {
		tw = size.Width
	}
	maxOff := b.content - b.view
	tx := float32(0)
	if maxOff > 0 {
		tx = (size.Width - tw) * (b.offset / maxOff)
	}
	b.thumb.Move(fyne.NewPos(tx, y))
	b.thumb.Resize(fyne.NewSize(tw, barH))
}

func (r *hscrollBarRenderer) MinSize() fyne.Size {
	return fyne.NewSize(minThumb, scrollbarRowH)
}

func (r *hscrollBarRenderer) Refresh() {
	r.Layout(r.bar.Size())
	canvas.Refresh(r.bar.track)
	canvas.Refresh(r.bar.thumb)
}
