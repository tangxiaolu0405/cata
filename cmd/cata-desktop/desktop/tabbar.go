package desktop

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// tabHeight 标签页高度（比 split 模式标题栏 titleBarHeight 更高，参考
// VSCode：Tab 模式标签更高，更好点按/拖动）。
const tabHeight = 44

// 标签页宽度范围：标题短则按内容自适应，长则省略号截断（与 split 模式
// 标题栏一致：不铺满、定长上限）。最小宽度给足标题可抓取/可读区域：
// 太窄时 ✕ 会挤占大半标签，标题难以点住拖动。
const (
	tabMinWidth = 140
	tabMaxWidth = 240
)

// 标签页内边距：左右 8px，上下 4px。
const (
	tabPadX = 8
	tabPadY = 4
)

// tabCloseSize ✕ 按钮的固定边长（GridWrap 定长，比默认按钮小，给标题让位）。
const tabCloseSize = 22

// tabDrag 标签拖动重排的瞬时状态（只存在于一次拖动中）：
//   - item：被拖动的标签项（拖动时移到标签行末尾，绘制在最上层，浮在其它标签上方）
//   - from：被拖动标签的原始下标
//   - to：当前目标槽位（0..n-1，其余标签让出等宽空隙）
//   - dx：累计水平位移（拖动项跟随光标 = 原始起点 + dx）
//   - origX/ws：拖动开始时各标签的原始起点/宽度快照
type tabDrag struct {
	item  fyne.CanvasObject // 被拖动的标签项（移到 Objects 末尾，绘制在最上层）
	from  int
	to    int
	dx    float32
	origX []float32
	ws    []float32
}

// tabRowLayout 把标签页「挨着排列」：每个标签按自身宽度紧贴排成一行，
// 无间距；标签总宽超过可用宽度时由外层 HScroll 横向滑动。
//
// 拖动重排（参考 VSCode）：拖动中「被拖动标签实时跟随光标」浮在最上层，
// 其余标签在目标槽位让出一个等宽空隙；目标下标变化时空隙移动，标签让位。
// 布局不重建标签项，只按当前 tabDrag 重新计算位置，拖动会话不中断。
type tabRowLayout struct {
	// getDrag 返回当前拖动状态；nil = 未在拖动（普通挨着排列）。
	// 用回调而非直接持有指针，是因为 Container 持有的是本布局的副本，
	// 直接改字段不会生效。
	getDrag func() *tabDrag
}

func (l tabRowLayout) drag() *tabDrag {
	if l.getDrag == nil {
		return nil
	}
	return l.getDrag()
}

func (l tabRowLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	if d := l.drag(); d != nil {
		l.layoutDrag(objs, size, d)
		return
	}
	l.layoutPlain(objs, size)
}

// layoutPlain 普通排列：按各自 MinSize 宽度挨着排成一行。
func (l tabRowLayout) layoutPlain(objs []fyne.CanvasObject, size fyne.Size) {
	x := float32(0)
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		w := o.MinSize().Width
		o.Move(fyne.NewPos(x, 0))
		o.Resize(fyne.NewSize(w, size.Height))
		x += w
	}
}

// layoutDrag 拖动排列（参考 VSCode）：
//   - 除被拖动项外的标签按原顺序排开，并在目标槽位 to 留出一个等宽空隙；
//   - 被拖动项「跟随光标」：原始起点 + 累计位移，夹在行内；
//   - 被拖动项在 Objects 末尾，绘制在最上层（浮在其它标签上方）。
func (l tabRowLayout) layoutDrag(objs []fyne.CanvasObject, size fyne.Size, d *tabDrag) {
	n := len(objs)
	if d == nil || d.item == nil || d.from < 0 || d.from >= n {
		l.layoutPlain(objs, size)
		return
	}
	to := d.to
	if to < 0 {
		to = 0
	} else if to >= n {
		to = n - 1
	}

	// 其余标签（原始下标顺序）排开，目标槽位插入等宽空隙。
	restX := make([]float32, n)
	x := float32(0)
	placed := 0
	for i := 0; i < n; i++ {
		if i == d.from {
			continue
		}
		if placed == to { // 空隙插在 rest[to] 之前
			x += d.ws[d.from]
		}
		restX[i] = x
		x += d.ws[i]
		placed++
	}
	if to == n-1 { // 目标在末尾：空隙在最后
		x += d.ws[d.from]
	}
	total := x

	// 被拖动项跟随光标：原始起点 + 累计位移（夹在行内）。
	dragX := d.origX[d.from] + d.dx
	if dragX < 0 {
		dragX = 0
	}
	if dragX+d.ws[d.from] > total {
		dragX = total - d.ws[d.from]
	}

	// 按原始下标给每个标签定位（Objects 顺序已被拖动项移到末尾）。
	for orig := 0; orig < n; orig++ {
		var o fyne.CanvasObject
		if orig == d.from {
			o = d.item
		} else {
			ni := orig
			if orig > d.from {
				ni--
			}
			o = objs[ni]
		}
		if !o.Visible() {
			continue
		}
		px := restX[orig]
		if o == d.item {
			px = dragX
		}
		o.Move(fyne.NewPos(px, 0))
		o.Resize(fyne.NewSize(d.ws[orig], size.Height))
	}
}

func (l tabRowLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	w := float32(0)
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		w += o.MinSize().Width
	}
	return fyne.NewSize(w, tabHeight)
}

// tabDragTarget 由原始起点/宽度与累计位移，算出被拖动标签的目标下标：
//   - 向右：拖动项「右边缘」越过相邻标签「中心」→ 换位
//   - 向左：拖动项「左边缘」越过相邻标签「中心」→ 换位
//
// 未越过保持原下标。origX/ws 为未拖动时的各标签起点与宽度，d 为被拖动标签下标。
func tabDragTarget(origX, ws []float32, d int, dx float32) int {
	n := len(ws)
	if d < 0 || d >= n {
		return d
	}
	t := d
	if dx > 0 {
		right := origX[d] + ws[d] + dx // 拖动项右边缘
		for i := d + 1; i < n; i++ {
			if right > origX[i]+ws[i]/2 {
				t = i
			} else {
				break
			}
		}
	} else if dx < 0 {
		left := origX[d] + dx // 拖动项左边缘
		for i := d - 1; i >= 0; i-- {
			if left < origX[i]+ws[i]/2 {
				t = i
			} else {
				break
			}
		}
	}
	return t
}

// tabItem 一个标签页：背景 + 标题 + ✕。
//   - 点击标签任意处（除 ✕）→ onSelect（切换激活）
//   - 点击 ✕ → onClose（关闭窗口）
//   - 按住横向拖动 → onDrag / onDragEnd（重排标签顺序）
//   - 激活标签背景 = InputBackground（同 split 标题栏），底部 2px 主色条；
//     未激活 = 窗口背景 + 右侧 1px 分隔线（VSCode 风格：激活 tab 与内容区连成一体）
type tabItem struct {
	widget.BaseWidget

	label     *widget.Label
	close     *widget.Button
	closeWrap *fyne.Container // 固定 22×22 的 ✕（GridWrap 定长）
	bg        *canvas.Rectangle
	accent    *canvas.Rectangle // 激活底部主色条
	sep       *canvas.Rectangle // 未激活右侧 1px 分隔线

	active    bool
	onSelect  func()
	onClose   func()
	onDrag    func(dx float32) // 拖动中：dx 为本次移动增量
	onDragEnd func()
	dragging  bool // 正在被拖动（拖动中显示高亮）
}

func newTabItem(title string, active bool, onSelect, onClose func()) *tabItem {
	t := &tabItem{
		label:    widget.NewLabel(title),
		active:   active,
		onSelect: onSelect,
		onClose:  onClose,
	}
	t.label.Truncation = fyne.TextTruncateEllipsis
	t.close = widget.NewButtonWithIcon("", theme.CancelIcon(), onClose)
	t.close.Importance = widget.LowImportance
	t.closeWrap = container.NewGridWrap(fyne.NewSize(tabCloseSize, tabCloseSize), t.close)
	t.bg = canvas.NewRectangle(theme.Color(theme.ColorNameBackground))
	t.accent = canvas.NewRectangle(theme.Color(theme.ColorNamePrimary))
	t.sep = canvas.NewRectangle(theme.Color(theme.ColorNameSeparator))
	t.ExtendBaseWidget(t)
	return t
}

func (t *tabItem) Tapped(_ *fyne.PointEvent) {
	if t.onSelect != nil {
		t.onSelect()
	}
}

// Cursor 标题区域显示水平拖动光标，提示「按住可拖动重排」。
// （✕ 按钮自身 Cursor=Default，不覆盖。）
func (t *tabItem) Cursor() desktop.Cursor {
	return desktop.HResizeCursor
}

// Dragged 拖动中：标记拖动态并转发增量位移给 App（App 负责实时重排）。
// 拖动超过阈值后 Fyne 不再派发 Tapped，点击（未拖动）仍是切换激活。
func (t *tabItem) Dragged(e *fyne.DragEvent) {
	t.dragging = true
	canvas.Refresh(t)
	if t.onDrag != nil {
		t.onDrag(e.Dragged.DX)
	}
}

// DragEnd 拖动结束：清除拖动态，通知 App 落定（重排或还原）。
func (t *tabItem) DragEnd() {
	t.dragging = false
	canvas.Refresh(t)
	if t.onDragEnd != nil {
		t.onDragEnd()
	}
}

var _ fyne.Tappable = (*tabItem)(nil)
var _ fyne.Draggable = (*tabItem)(nil)
var _ desktop.Cursorable = (*tabItem)(nil)

func (t *tabItem) CreateRenderer() fyne.WidgetRenderer {
	return &tabItemRenderer{
		objects: []fyne.CanvasObject{t.bg, t.accent, t.sep, t.label, t.closeWrap},
		item:    t,
	}
}

// tabItemRenderer 手写渲染器：背景/激活条/分隔线 + 标题 + ✕ 的定位。
type tabItemRenderer struct {
	objects []fyne.CanvasObject
	item    *tabItem
}

func (r *tabItemRenderer) Destroy() {}

func (r *tabItemRenderer) Objects() []fyne.CanvasObject { return r.objects }

func (r *tabItemRenderer) MinSize() fyne.Size {
	i := r.item
	w := clampTabWidth(i.label.MinSize().Width + i.closeWrap.MinSize().Width + tabPadX*2)
	h := i.label.MinSize().Height + tabPadY*2
	if h < tabHeight {
		h = tabHeight
	}
	return fyne.NewSize(w, h)
}

// clampTabWidth 标签宽度夹紧到 [tabMinWidth, tabMaxWidth]：标题短按内容，
// 长则省略号截断（不铺满、有定长上限，与 split 标题栏一致）。
func clampTabWidth(w float32) float32 {
	if w < tabMinWidth {
		return tabMinWidth
	}
	if w > tabMaxWidth {
		return tabMaxWidth
	}
	return w
}

func (r *tabItemRenderer) Layout(size fyne.Size) {
	i := r.item
	th := theme.CurrentForWidget(i)
	variant := fyne.CurrentApp().Settings().ThemeVariant()

	switch {
	case i.dragging:
		// 拖动中高亮：hover 底色 + 底部主色条（无论是否激活），一眼看出在拖哪个。
		i.bg.FillColor = th.Color(theme.ColorNameHover, variant)
		i.accent.Show()
		i.sep.Hide()
	case i.active:
		i.bg.FillColor = th.Color(theme.ColorNameInputBackground, variant)
		i.accent.Show()
		i.sep.Hide()
	default:
		i.bg.FillColor = th.Color(theme.ColorNameBackground, variant)
		i.accent.Hide()
		i.sep.Show()
	}
	i.bg.Move(fyne.NewPos(0, 0))
	i.bg.Resize(size)

	i.accent.Move(fyne.NewPos(0, size.Height-2))
	i.accent.Resize(fyne.NewSize(size.Width, 2))

	i.sep.Move(fyne.NewPos(size.Width-1, 0))
	i.sep.Resize(fyne.NewSize(1, size.Height))

	closeW := i.closeWrap.MinSize().Width
	closeH := i.closeWrap.MinSize().Height
	i.closeWrap.Move(fyne.NewPos(size.Width-closeW-tabPadX, (size.Height-closeH)/2))
	i.closeWrap.Resize(fyne.NewSize(closeW, closeH))

	i.label.Move(fyne.NewPos(tabPadX, 0))
	i.label.Resize(fyne.NewSize(size.Width-closeW-tabPadX*2, size.Height))
}

func (r *tabItemRenderer) Refresh() {
	r.Layout(r.item.Size())
	canvas.Refresh(r.item.bg)
	canvas.Refresh(r.item.accent)
	canvas.Refresh(r.item.sep)
	canvas.Refresh(r.item.label)
	canvas.Refresh(r.item.closeWrap)
}
