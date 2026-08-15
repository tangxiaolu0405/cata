package desktop

import (
	"fyne.io/fyne/v2"
)

// fixedSplitLayout 固定宽度左右分栏：左侧固定 leftWidth 像素（工作空间树），
// 中间 1px 分隔线，右侧自适应（多窗口平铺区）。不可拖动。
type fixedSplitLayout struct {
	leftWidth float32
}

func (l *fixedSplitLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	if len(objs) < 3 {
		return
	}
	left, div, right := objs[0], objs[1], objs[2]
	const divider = 1
	left.Move(fyne.NewPos(0, 0))
	left.Resize(fyne.NewSize(l.leftWidth, size.Height))
	div.Move(fyne.NewPos(l.leftWidth, 0))
	div.Resize(fyne.NewSize(divider, size.Height))
	right.Move(fyne.NewPos(l.leftWidth+divider, 0))
	w := size.Width - l.leftWidth - divider
	if w < 0 {
		w = 0
	}
	right.Resize(fyne.NewSize(w, size.Height))
}

// MinSize 固定有界值，不由子内容派生：任何子内容（长文件、长路径标题）都不会把窗口撑大。
func (l *fixedSplitLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(l.leftWidth+1+titleBarWidth, 240)
}

// tileLayout 多窗口并排布局：每个窗口保持固定最小宽度（minW），放不下时
// 由外层 HScroll 在窗口之间横向滑动（滑动的是窗口，不是标题栏）。
//   - 窗口总宽 ≤ 可用宽度：等宽铺满右侧剩余区域；关掉任意一个，其余自动重排占满；
//   - 窗口总宽 > 可用宽度：每个窗口保持 minW 定宽，横向滚动查看后面的窗口；
//   - 全部关闭：显示 empty 占位。
//
// 对象列表 = [面板0, 面板1, ..., 面板N, empty]。面板之间的分隔线由
// 各面板自带 1px 右分隔线构成，布局本身不再插入分隔对象。不可拖动。
type tileLayout struct {
	minWidth float32 // 每个窗口的最小宽度；0 时用 titleBarWidth
}

func (l *tileLayout) minW() float32 {
	if l.minWidth > 0 {
		return l.minWidth
	}
	return titleBarWidth
}

func (l *tileLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	if len(objs) == 0 {
		return
	}
	empty := objs[len(objs)-1]
	panels := objs[:len(objs)-1]

	var vis []fyne.CanvasObject
	for _, o := range panels {
		if o.Visible() {
			vis = append(vis, o)
		}
	}

	off := func(o fyne.CanvasObject) {
		o.Move(fyne.NewPos(size.Width, 0))
		o.Resize(fyne.NewSize(0, 0))
	}

	if len(vis) == 0 {
		empty.Show()
		empty.Move(fyne.NewPos(0, 0))
		empty.Resize(size)
		for _, o := range panels {
			off(o)
		}
		return
	}

	empty.Hide()
	n := len(vis)
	minW := l.minW()
	w := size.Width / float32(n)
	if w < minW {
		w = minW // 放不下：每个窗口定宽 minW，靠外层 HScroll 横向滑动
	}
	for i, o := range vis {
		o.Move(fyne.NewPos(float32(i)*w, 0))
		o.Resize(fyne.NewSize(w, size.Height))
	}
}

// MinSize 返回内容最小尺寸：宽 = 可见窗口数 × minW（固定值，不随子内容
// 变化），高固定 240。外层 HScroll 据此判断内容是否超出视口、是否需要滚动。
func (l *tileLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	n := 0
	if len(objs) > 0 {
		for _, o := range objs[:len(objs)-1] {
			if o.Visible() {
				n++
			}
		}
	}
	minW := l.minW()
	w := minW * float32(n)
	if w < minW {
		w = minW
	}
	return fyne.NewSize(w, 240)
}

// hbarOverlayLayout 把水平滚动条放在窗口标题行正下方：Y = titleBarHeight，
// 横跨整个平铺区宽度。平铺区（可横向滑动的 HScroll）是 Stack 的底层，
// 滚动条叠在其上；每个面板在标题下预留了 scrollbarRowH 空隙，因此滚动条
// 正好落在空隙里，不遮挡内容。
type hbarOverlayLayout struct{}

func (l *hbarOverlayLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	if len(objs) == 0 {
		return
	}
	bar, ok := objs[0].(hbarLike)
	if !ok {
		return
	}
	bar.setView(size.Width)
	bar.Move(fyne.NewPos(0, titleBarHeight))
	bar.Resize(fyne.NewSize(size.Width, scrollbarRowH))
	bar.sync()
}

func (l *hbarOverlayLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(1, scrollbarRowH)
}
