package desktop

import (
	"testing"

	"fyne.io/fyne/v2"
)

// sizedObject 带可设定 MinSize 的 mock 对象（复用 layouts_test.go 的 trackingObject）。
type sizedObject struct {
	trackingObject
	min fyne.Size
}

func (o *sizedObject) MinSize() fyne.Size { return o.min }

// TestTabRowLayoutAdjacent 验证：标签挨着排列（无间距），宽度按各自 MinSize，
// 高度填满整行；MinSize = 各标签宽之和。
func TestTabRowLayoutAdjacent(t *testing.T) {
	l := tabRowLayout{}
	tabs := []fyne.CanvasObject{
		&sizedObject{trackingObject: trackingObject{visible: true}, min: fyne.NewSize(100, 30)},
		&sizedObject{trackingObject: trackingObject{visible: true}, min: fyne.NewSize(150, 30)},
		&sizedObject{trackingObject: trackingObject{visible: true}, min: fyne.NewSize(80, 30)},
	}
	// 空占位：最后一对象不可见（模拟无窗口）
	tabs = append(tabs, &trackingObject{visible: false})

	l.Layout(tabs, fyne.NewSize(500, tabHeight))
	expects := []struct{ x, w float32 }{{0, 100}, {100, 150}, {250, 80}}
	for i, e := range expects {
		to := tabs[i].(*sizedObject)
		if to.pos.X != e.x {
			t.Fatalf("tab %d X = %v, want %v（挨着排列无间距）", i, to.pos.X, e.x)
		}
		if to.size.Width != e.w {
			t.Fatalf("tab %d width = %v, want %v", i, to.size.Width, e.w)
		}
		if to.size.Height != tabHeight {
			t.Fatalf("tab %d height = %v, want %v（填满整行）", i, to.size.Height, tabHeight)
		}
	}

	if m := l.MinSize(tabs); m != fyne.NewSize(100+150+80, tabHeight) {
		t.Fatalf("MinSize = %v, want %v", m, fyne.NewSize(100+150+80, tabHeight))
	}
}

// TestTabRowLayoutMinSizeIgnoresHidden 验证：隐藏的标签不计入 MinSize。
func TestTabRowLayoutMinSizeIgnoresHidden(t *testing.T) {
	l := tabRowLayout{}
	tabs := []fyne.CanvasObject{
		&sizedObject{trackingObject: trackingObject{visible: true}, min: fyne.NewSize(100, 30)},
		&trackingObject{visible: false},
	}
	if m := l.MinSize(tabs); m.Width != 100 {
		t.Fatalf("MinSize width = %v, want 100（隐藏标签不计入）", m.Width)
	}
}

// TestClampTabWidth 验证：标签宽度夹紧在 [tabMinWidth, tabMaxWidth]。
func TestClampTabWidth(t *testing.T) {
	cases := []struct {
		in, want float32
	}{
		{10, tabMinWidth}, // 太短 → 最小
		{tabMinWidth, tabMinWidth},
		{160, 160}, // 正常
		{tabMaxWidth, tabMaxWidth},
		{500, tabMaxWidth}, // 太长 → 截断上限
	}
	for _, c := range cases {
		if got := clampTabWidth(c.in); got != c.want {
			t.Fatalf("clampTabWidth(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// mkTabs 构造 n 个可见标签：宽 w[i]（下标即原始位置）。
func mkTabs(ws ...float32) []*sizedObject {
	out := make([]*sizedObject, len(ws))
	for i, w := range ws {
		out[i] = &sizedObject{trackingObject: trackingObject{visible: true}, min: fyne.NewSize(w, 30)}
	}
	return out
}

// tabMetrics 按未拖动顺序计算各标签的原始起点与宽度（与 app.go 拖动首帧快照一致）。
func tabMetrics(tabs []*sizedObject) (origX, ws []float32) {
	origX = make([]float32, len(tabs))
	ws = make([]float32, len(tabs))
	x := float32(0)
	for i, t := range tabs {
		ws[i] = t.min.Width
		origX[i] = x
		x += ws[i]
	}
	return
}

// canvasObjects 把 *sizedObject 列表转成 []fyne.CanvasObject（传给布局）。
func canvasObjects(tabs []*sizedObject) []fyne.CanvasObject {
	out := make([]fyne.CanvasObject, len(tabs))
	for i, t := range tabs {
		out[i] = t
	}
	return out
}

// assertTabX 按「原始下标」断言各标签布局后的 X（拖动测试：Objects 顺序里
// 被拖动项在末尾，但期望按面板顺序 readable）。
func assertTabX(t *testing.T, tabs []*sizedObject, want []float32, msg string) {
	t.Helper()
	for i, x := range want {
		if got := tabs[i].pos.X; got != x {
			t.Fatalf("%s: tab %d X = %v, want %v", msg, i, got, x)
		}
	}
}

// TestTabRowLayoutDragForward 验证：拖动标签向右越过相邻标签中心后，其余
// 标签实时让位、被拖动标签「跟随光标」浮在最上层（from=1 → to=3）。
func TestTabRowLayoutDragForward(t *testing.T) {
	tabs := mkTabs(100, 20, 30, 40)
	origX, ws := tabMetrics(tabs)
	drag := &tabDrag{item: tabs[1], from: 1, to: 3, dx: 60, origX: origX, ws: ws}
	l := tabRowLayout{getDrag: func() *tabDrag { return drag }}
	// Objects 顺序：被拖动项已移到末尾 [t0,t2,t3,t1]。
	objs := []fyne.CanvasObject{tabs[0], tabs[2], tabs[3], tabs[1]}
	l.Layout(objs, fyne.NewSize(500, tabHeight))

	// 原始下标位置：t0=0、t2=100、t3=130 排开（末尾留空隙），t1 跟随光标=100+60=160。
	assertTabX(t, tabs, []float32{0, 160, 100, 130}, "拖动 from=1→to=3")
}

// TestTabRowLayoutDragBackward 验证：拖动标签向左（from=3 → to=1）。
func TestTabRowLayoutDragBackward(t *testing.T) {
	tabs := mkTabs(100, 20, 30, 40)
	origX, ws := tabMetrics(tabs)
	drag := &tabDrag{item: tabs[3], from: 3, to: 1, dx: -50, origX: origX, ws: ws}
	l := tabRowLayout{getDrag: func() *tabDrag { return drag }}
	// 被拖动项本身在末尾，Objects 顺序不变 [t0,t1,t2,t3]。
	objs := canvasObjects(tabs)
	l.Layout(objs, fyne.NewSize(500, tabHeight))

	// 原始下标位置：t0=0，t1 让位后=140、t2=160，t3 跟随光标=150-50=100。
	assertTabX(t, tabs, []float32{0, 140, 160, 100}, "拖动 from=3→to=1")
}

// TestTabRowLayoutDragNoMove 验证：拖动但目标未变（from==to）时，其余标签
// 位置与普通排列一致，被拖动项也回到原始起点（dx=0）。
func TestTabRowLayoutDragNoMove(t *testing.T) {
	tabs := mkTabs(100, 20, 30, 40)
	origX, ws := tabMetrics(tabs)
	drag := &tabDrag{item: tabs[1], from: 1, to: 1, dx: 0, origX: origX, ws: ws}
	l := tabRowLayout{getDrag: func() *tabDrag { return drag }}
	objs := []fyne.CanvasObject{tabs[0], tabs[2], tabs[3], tabs[1]}
	l.Layout(objs, fyne.NewSize(500, tabHeight))

	assertTabX(t, tabs, []float32{0, 100, 120, 150}, "from==to 不移动")
}

// TestTabRowLayoutDragNilState 验证：getDrag 返回 nil 时退化为普通排列
// （Objects 按面板顺序，无被拖动项浮层）。
func TestTabRowLayoutDragNilState(t *testing.T) {
	l := tabRowLayout{getDrag: func() *tabDrag { return nil }}
	tabs := mkTabs(100, 20, 30, 40)
	l.Layout(canvasObjects(tabs), fyne.NewSize(500, tabHeight))

	assertTabX(t, tabs, []float32{0, 100, 120, 150}, "无拖动状态")
}

// TestTabDragTarget 验证目标下标判定（VSCode 式边缘判定）：
//   - 向右：拖动项「右边缘」越过相邻标签「中心」→ 换位
//   - 向左：拖动项「左边缘」越过相邻标签「中心」→ 换位
func TestTabDragTarget(t *testing.T) {
	// 四个标签：宽 100、20、30、40 → 起点 0、100、120、150。
	origX := []float32{0, 100, 120, 150}
	ws := []float32{100, 20, 30, 40}

	cases := []struct {
		name string
		d    int
		dx   float32
		want int
	}{
		{"未移动", 1, 0, 1},
		{"右移未越过 tab2 中心", 1, 16, 2},  // 右边缘 100+20+16=136 > tab2 中心 135 → 2
		{"右移越过 tab3 中心", 1, 60, 3},   // 右边缘 180 > tab3 中心 170 → 3
		{"左移越过 tab0 中心", 1, -100, 0}, // 左边缘 100-100=0 < tab0 中心 50 → 0
		{"右移到底", 0, 999, 3},
		{"左移到底", 3, -300, 0},
		{"左移越过 tab1 中心", 2, -25, 1}, // 左边缘 120-25=95 < tab1 中心 110 → 1
	}
	for _, c := range cases {
		if got := tabDragTarget(origX, ws, c.d, c.dx); got != c.want {
			t.Fatalf("%s: tabDragTarget(d=%d, dx=%v) = %d, want %d", c.name, c.d, c.dx, got, c.want)
		}
	}
}

// TestMoveSliceItem 验证重排辅助函数：把 from 移动到 to，其余保持相对顺序。
func TestMoveSliceItem(t *testing.T) {
	in := []int{0, 1, 2, 3, 4}

	cases := []struct {
		from, to int
		want     []int
	}{
		{1, 3, []int{0, 2, 3, 1, 4}}, // 右移
		{3, 1, []int{0, 3, 1, 2, 4}}, // 左移
		{2, 2, []int{0, 1, 2, 3, 4}}, // 不动
		{-1, 2, []int{0, 1, 2, 3, 4}},
		{2, 9, []int{0, 1, 2, 3, 4}},
	}
	for _, c := range cases {
		got := moveSliceItem(append([]int(nil), in...), c.from, c.to)
		if len(got) != len(c.want) {
			t.Fatalf("moveSliceItem(%d→%d) len = %d, want %d", c.from, c.to, len(got), len(c.want))
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Fatalf("moveSliceItem(%d→%d) = %v, want %v", c.from, c.to, got, c.want)
			}
		}
	}
}
