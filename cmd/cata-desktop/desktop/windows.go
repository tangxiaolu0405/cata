package desktop

// 本文件集中窗口（Tab / Split）呈现与管理的逻辑，从 app.go 拆出，
// 减轻 App 上帝对象的职责面；行为与之前完全一致。

import (
	"fyne.io/fyne/v2"
)

// ---------- 窗口（Tab / Split 两种呈现） ----------

// setMode 切换 Tab / Split 呈现（「窗口」菜单：分割窗口 / 合并窗口）。
// 切换前先把 content 从当前呈现的树里摘掉，避免同一对象同时挂在两棵树。
func (a *App) setMode(m viewMode) {
	if a.mode == m {
		return
	}
	a.mode = m
	switch m {
	case modeSplit:
		a.tabContent.Objects = nil // 内容移回各窗口外壳
		a.tabContent.Refresh()
	case modeTab:
		a.tiles.Objects = []fyne.CanvasObject{a.empty} // 内容移入 tab 内容区
		a.tiles.Refresh()
		for _, p := range a.panels {
			p.box = nil
		}
	}
	a.applyMode()
	a.refreshPanels()
}

// applyMode 只切换两种呈现的显隐（不重建内容；数据变化走 refreshPanels）。
func (a *App) applyMode() {
	switch a.mode {
	case modeSplit:
		if a.tabStack != nil {
			a.tabStack.Hide()
		}
		if a.splitStack != nil {
			a.splitStack.Show()
		}
	case modeTab:
		if a.splitStack != nil {
			a.splitStack.Hide()
		}
		if a.tabStack != nil {
			a.tabStack.Show()
		}
	}
}

// refreshPanels 按当前模式重建右侧内容：Split 重排并排窗口，Tab 重建标签栏。
func (a *App) refreshPanels() {
	if a.mode == modeSplit {
		a.rebuildTiles()
	} else {
		a.rebuildTabBar()
	}
}

// rebuildTiles 把当前 panels 重新装进平铺区并触发重排（Split 模式）。
// 关闭/打开窗口后调用，剩余窗口会自动重排占满（tileLayout 按可见窗口等分）。
func (a *App) rebuildTiles() {
	objs := make([]fyne.CanvasObject, 0, len(a.panels)+1)
	for _, p := range a.panels {
		if p.box == nil {
			p.buildBox()
		}
		objs = append(objs, p.box)
	}
	objs = append(objs, a.empty)
	a.tiles.Objects = objs
	a.tiles.Refresh()
	a.updateHScrollBar()
}

// updateHScrollBar 让 HScroll 按新的面板数量重新布局/夹紧偏移，并同步
// 自绘滚动条的内容宽度与显示状态（溢出时显示，放得下时隐藏）。
func (a *App) updateHScrollBar() {
	if a.tilesScroll == nil || a.tiles == nil || a.hbar == nil {
		return
	}
	a.tilesScroll.Refresh()
	a.hbar.setContent(a.tiles.MinSize().Width)
}

// rebuildTabBar 重建标签栏与激活内容区（Tab 模式）：所有窗口标题连成一行，
// 激活窗口的 content 放进内容区占满，底部路径栏显示激活窗口的完整路径。
func (a *App) rebuildTabBar() {
	items := make([]fyne.CanvasObject, 0, len(a.panels))
	for i, p := range a.panels {
		p := p
		i := i
		it := newTabItem(p.title.Text, p == a.active,
			func() { a.activatePanel(p) },
			func() { a.closePanel(p) })
		it.onDrag = func(dx float32) { a.onTabDragged(it, i, dx) }
		it.onDragEnd = func() { a.onTabDragEnd() }
		items = append(items, it)
	}
	a.tabRow.Objects = items
	a.tabRow.Refresh()
	if a.tabScroll != nil {
		a.tabScroll.Refresh()
	}

	var content fyne.CanvasObject = a.tabEmpty
	if a.active != nil {
		content = a.active.content
		a.tabFooter.SetText(a.active.footer.Text)
	} else {
		a.tabFooter.SetText("")
	}
	a.tabContent.Objects = []fyne.CanvasObject{content}
	a.tabContent.Refresh()
}

// onTabDragged 标签拖动中（Tab 模式）：被拖动标签实时跟随光标浮在最上层，
// 其余标签在目标槽位让出等宽空隙。dx 为本次移动增量；拖动期间不重建标签项
// （只刷新布局），保证 Fyne 的拖动会话不中断。
func (a *App) onTabDragged(it *tabItem, idx int, dx float32) {
	if a.tabDrag == nil {
		// 首次拖动：快照各标签原始起点/宽度（此刻 Objects 按 panels 顺序），
		// 并把被拖动标签移到 Objects 末尾（其余保持相对顺序），使其绘制在最上层。
		n := len(a.tabRow.Objects)
		origX := make([]float32, n)
		ws := make([]float32, n)
		x := float32(0)
		for i, o := range a.tabRow.Objects {
			w := o.MinSize().Width
			ws[i] = w
			origX[i] = x
			x += w
		}
		objs := a.tabRow.Objects
		rest := make([]fyne.CanvasObject, 0, n)
		rest = append(rest, objs[:idx]...)
		rest = append(rest, objs[idx+1:]...)
		rest = append(rest, objs[idx])
		a.tabRow.Objects = rest
		a.tabDrag = &tabDrag{item: it, from: idx, to: idx, origX: origX, ws: ws}
	}
	a.tabDrag.dx += dx
	a.updateTabDragTarget()
}

// updateTabDragTarget 用拖动快照（原始起点/宽度/累计位移）算出新的目标下标；
// 变化时刷新标签行布局，让被拖动标签实时滑入新位置。
func (a *App) updateTabDragTarget() {
	d := a.tabDrag
	if d == nil || d.from < 0 || d.from >= len(d.ws) {
		return
	}
	t := tabDragTarget(d.origX, d.ws, d.from, d.dx)
	if t != d.to {
		d.to = t
		a.tabRow.Refresh()
	}
}

// onTabDragEnd 标签拖动结束：清空拖动状态；若目标位置变化则重排窗口顺序，
// 并统一重建标签栏（恢复 Objects 顺序、落定）。
func (a *App) onTabDragEnd() {
	if a.tabDrag == nil {
		return
	}
	d, t := a.tabDrag.from, a.tabDrag.to
	a.tabDrag = nil
	if t >= 0 && t < len(a.panels) && t != d {
		a.panels = moveSliceItem(a.panels, d, t)
	}
	a.rebuildTabBar()
}

// moveSliceItem 返回把 s 中下标 from 的元素移动到下标 to 的新切片
// （其余元素保持相对顺序；下标越界或相等时原样返回）。
func moveSliceItem[T any](s []T, from, to int) []T {
	if from == to || from < 0 || from >= len(s) || to < 0 || to >= len(s) {
		return s
	}
	item := s[from]
	s = append(s[:from], s[from+1:]...)
	s = append(s[:to], append([]T{item}, s[to:]...)...)
	return s
}

// activatePanel 激活指定窗口（Tab 模式：点击标签切换；Split 模式仅记录，
// 供下次切回 Tab 时作为激活项）。
func (a *App) activatePanel(p *panel) {
	if a.active == p {
		return
	}
	a.active = p
	if a.mode == modeTab {
		a.rebuildTabBar()
	}
}
