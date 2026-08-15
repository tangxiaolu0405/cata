// Package desktop 是 cata-desktop 的 Fyne 桌面应用。
//
// 定位：工作空间浏览器 + 多文件查看器 + 多内嵌终端。不侵入 cata 核心——
// 只读 link.json（工作空间注册）与文件系统；LLM/对话交给终端里的
// `cata`（TUI）。纯 Go + Fyne，无 WebView，跨平台（macOS/Win/Linux）。
package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// wsPrefix 工作空间树节点的 UID 前缀：ws:<root_path>。
// 文件节点 UID 是绝对路径（以 / 或盘符开头），不会与 ws: 冲突。
const wsPrefix = "ws:"

// viewMode 右侧内容区的两种呈现（参考 VSCode）：
//   - modeTab（默认）：所有窗口标题连成一行标签栏，激活窗口内容占满全部空间；
//   - modeSplit（分割）：窗口并排平铺，各自标题栏 + 内容（历史行为）。
type viewMode int

const (
	modeTab viewMode = iota
	modeSplit
)

// App 桌面应用：持有 Fyne 组件与工作空间/文件浏览状态。
type App struct {
	fyneApp fyne.App
	win     fyne.Window

	// UI 引用
	tree   *widget.Tree
	panels []*panel // 已打开的窗口（文件查看器 / 内嵌终端）

	// Split 模式组件：右侧多窗口平铺区（tileLayout）+ HScroll + 自绘滚动条。
	tiles       *fyne.Container   // 多窗口平铺区（tileLayout）
	tilesScroll *container.Scroll // 平铺区 HScroll：负责窗口间横向滑动与滚轮
	hbar        *hscrollBar       // 自绘水平滚动条（标题栏正下方）
	hbarLayer   *fyne.Container   // 滚动条叠放层（hbarOverlayLayout）
	empty       fyne.CanvasObject
	splitStack  *fyne.Container // 整个 split 区域（HScroll + 滚动条叠放层）

	// Tab 模式组件：标签栏 + 激活内容区 + 底部路径栏。
	tabRow     *fyne.Container   // 标签行（tabRowLayout，挨着排列）
	tabScroll  *container.Scroll // 标签行 HScroll（标签过多时横向滑动）
	tabStack   *fyne.Container   // 整个 tab 区域（标签栏 + 内容 + footer）
	tabContent *fyne.Container   // 激活窗口内容（占满）
	tabFooter  *widget.Label     // 激活窗口的完整路径
	tabEmpty   fyne.CanvasObject // 无窗口时的占位
	rightStack *fyne.Container   // 包住 tabStack + splitStack，按 mode 显隐

	// 状态
	workspaces []WorkspaceInfo
	cur        *WorkspaceInfo
	lastFile   string                 // 最近一次打开的文件路径（供「重新打开上次文件」）
	dirCache   map[string][]FileEntry // 目录 → 子项（懒加载缓存）
	dirLoaded  map[string]bool
	isDirCache map[string]bool
	mode       viewMode // modeTab（默认）/ modeSplit（分割）
	active     *panel   // Tab 模式的激活窗口
	tabDrag    *tabDrag // 标签拖动重排的瞬时状态（nil = 未在拖动）
}

// NewApp 构造应用。
func NewApp() *App {
	return &App{
		dirCache:   map[string][]FileEntry{},
		dirLoaded:  map[string]bool{},
		isDirCache: map[string]bool{},
	}
}

// Run 启动并阻塞运行（Fyne 主循环）。
func (a *App) Run() {
	// 声明已迁移到 fyne.Do 线程模型，避免 Fyne 打印弃用警告。
	app.SetMetadata(fyne.AppMetadata{
		ID:         "dev.cata.desktop",
		Name:       "Cata Desktop",
		Version:    "0.8.0",
		Build:      9,
		Migrations: map[string]bool{"fyneDo": true},
	})
	a.fyneApp = app.NewWithID("dev.cata.desktop")
	a.fyneApp.Settings().SetTheme(newCataTheme())

	a.win = a.fyneApp.NewWindow("Cata Desktop")
	a.win.Resize(fyne.NewSize(1280, 800))
	a.buildUI()
	a.refreshWorkspaces()
	// 启动默认内嵌终端（当前工作空间根目录）；其余窗口由点击文件/菜单打开。
	if a.cur != nil {
		a.addTermPanel()
	}
	// 退出时结束所有内嵌终端里的 shell，避免留下孤儿进程。
	a.win.SetCloseIntercept(func() {
		a.closeAllPanels()
		a.win.Close()
	})
	a.win.ShowAndRun()
}

// buildUI 组装窗口布局（无顶部工具栏；功能入口在系统菜单 + 快捷键）：
//
//	┌──────────────────────────────────────────────────────────────┐
//	│ 左：工作空间树（固定宽，工作空间为顶级文件夹）                  │
//	│ 右：Tab 模式（默认）：标签栏 + 激活内容占满                     │
//	│     Split 模式（⌘\）：多窗口并排平铺，各自标题栏带固定 ✕        │
func (a *App) buildUI() {
	// 左侧：单一工作空间树。顶级节点 = 工作空间（顶级文件夹），
	// 点击展开才是里面的文件与目录。
	a.tree = widget.NewTree(
		func(uid widget.TreeNodeID) []widget.TreeNodeID { return a.treeChildren(uid) },
		func(uid widget.TreeNodeID) bool { return a.treeBranch(uid) },
		func(bool) fyne.CanvasObject { return newNodeCell(a) },
		func(uid widget.TreeNodeID, _ bool, obj fyne.CanvasObject) {
			n := obj.(*nodeCell)
			n.uid = uid
			if uid == "" {
				n.name.SetText("…")
				n.path.Hide()
				return
			}
			if strings.HasPrefix(uid, wsPrefix) {
				if ws := a.wsForUID(uid); ws != nil {
					n.name.SetText(ws.Name)
					n.path.SetText(ws.RootPath)
					n.path.Show()
				} else {
					n.name.SetText("…")
					n.path.Hide()
				}
				return
			}
			n.name.SetText(filepath.Base(uid))
			n.path.Hide()
		},
	)
	a.tree.OnSelected = func(uid widget.TreeNodeID) { a.onTreeSelected(uid) }
	leftHeader := widget.NewLabelWithStyle("工作空间", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	left := container.NewBorder(leftHeader, nil, nil, nil, a.tree)

	// 右侧内容区：Tab 模式（默认）+ Split 模式两种呈现，按 mode 显隐。
	//   Tab 模式：标签栏（所有窗口标题连成一行，挨着排列）+ 激活窗口内容
	//             占满全部空间 + 底部路径栏；
	//   Split 模式：多窗口并排平铺，各自定长标题栏 + 内容（历史行为），
	//             窗口太多时整区横向滑动。
	a.empty = container.NewCenter(widget.NewLabel("没有打开的窗口\n点击左侧文件 → 新开查看器；「窗口」菜单可新建终端（⌘/Ctrl+2）"))
	a.tabEmpty = container.NewCenter(widget.NewLabel("没有打开的窗口\n点击左侧文件 → 新开查看器；「窗口」菜单可新建终端（⌘/Ctrl+2）"))

	// --- Split 模式：多窗口平铺区。初始只有一个占位（无窗口时显示）。
	// 每个窗口保持固定最小宽度（titleBarWidth），窗口太多放不下时整区
	// 横向滑动 —— 在窗口之间滑动，而不是在标题栏内部滑动。
	a.tiles = container.New(&tileLayout{minWidth: titleBarWidth}, a.empty)
	a.tilesScroll = container.NewHScroll(a.tiles)
	// Fyne 内置 HScroll 的滚动条固定在视口底部，无法移到标题下方：用
	// hiddenScrollTheme 把它隐藏，再叠放自绘 hscrollBar 到标题栏正下方，
	// 两者通过 offset 双向同步（滚轮/触控板走 HScroll，拖动滑块走 hscrollBar）。
	a.hbar = newHScrollBar(func(off float32) {
		a.tilesScroll.ScrollToOffset(fyne.NewPos(off, 0))
	})
	a.tilesScroll.OnScrolled = func(pos fyne.Position) {
		a.hbar.setOffset(pos.X)
	}
	a.hbarLayer = container.New(&hbarOverlayLayout{}, a.hbar)
	a.splitStack = container.NewStack(
		container.NewThemeOverride(a.tilesScroll, newHiddenScrollTheme()),
		a.hbarLayer,
	)

	// --- Tab 模式：标签栏 + 激活内容 + 底部路径栏。
	a.tabRow = container.New(tabRowLayout{getDrag: func() *tabDrag { return a.tabDrag }}) // 初始空，打开窗口后重建
	a.tabScroll = container.NewHScroll(a.tabRow)
	tabLine := canvas.NewRectangle(theme.Color(theme.ColorNameSeparator)) // 标签栏与内容的分隔线
	tabTop := container.NewBorder(nil, tabLine, nil, nil,
		container.NewThemeOverride(a.tabScroll, newHiddenScrollTheme()))
	a.tabFooter = widget.NewLabel("")
	a.tabFooter.Truncation = fyne.TextTruncateEllipsis
	a.tabFooter.Importance = widget.LowImportance
	footLine := canvas.NewRectangle(theme.Color(theme.ColorNameSeparator))
	footerBar := container.NewBorder(footLine, nil, nil, nil, a.tabFooter)
	a.tabContent = container.NewStack()
	a.tabStack = container.NewBorder(tabTop, footerBar, nil, nil, a.tabContent)

	a.rightStack = container.NewStack(a.tabStack, a.splitStack)
	a.mode = modeTab // 默认 Tab 模式
	a.applyMode()

	div1 := canvas.NewRectangle(theme.Color(theme.ColorNameSeparator))
	split := container.New(&fixedSplitLayout{leftWidth: 300}, left, div1, a.rightStack)

	a.win.SetContent(split)
	a.win.SetMainMenu(a.buildMainMenu())
}

// buildMainMenu 系统菜单（macOS 顶部菜单栏 / 其它平台窗口内菜单栏）：
// 文件操作与窗口开关从工具栏/标题栏分散到这里，并绑定快捷键。
func (a *App) buildMainMenu() *fyne.MainMenu {
	shortcut := func(key fyne.KeyName, mod fyne.KeyModifier) fyne.Shortcut {
		return &desktop.CustomShortcut{KeyName: key, Modifier: mod}
	}
	mod := fyne.KeyModifierShortcutDefault // macOS=⌘，其它平台=Ctrl

	file := fyne.NewMenu("文件",
		a.menuItem("添加目录…", shortcut(fyne.KeyO, mod), func() { a.addWorkspaceDialog() }),
		fyne.NewMenuItemSeparator(),
		a.menuItem("复制路径", shortcut(fyne.KeyC, mod|fyne.KeyModifierShift), func() { a.copyLastPath() }),
		a.menuItem("在文件管理器中显示", shortcut(fyne.KeyR, mod|fyne.KeyModifierShift), func() { a.revealLast() }),
		a.menuItem("系统打开", shortcut(fyne.KeyO, mod|fyne.KeyModifierShift), func() { a.openLast() }),
		fyne.NewMenuItemSeparator(),
		a.menuItem("退出", shortcut(fyne.KeyQ, mod), func() { a.fyneApp.Quit() }),
	)
	// 退出项放 macOS 应用菜单惯例位置：标记 IsQuit，并置于文件菜单末尾。
	file.Items[len(file.Items)-1].IsQuit = true

	winMenu := fyne.NewMenu("窗口",
		a.menuItem("重新打开上次文件", shortcut(fyne.Key1, mod), func() { a.reopenLastFile() }),
		a.menuItem("新建终端", shortcut(fyne.Key2, mod), func() { a.addTermPanel() }),
		fyne.NewMenuItemSeparator(),
		a.menuItem("分割窗口", shortcut(fyne.KeyBackslash, mod), func() { a.setMode(modeSplit) }),
		a.menuItem("合并窗口", shortcut(fyne.KeyBackslash, mod|fyne.KeyModifierShift), func() { a.setMode(modeTab) }),
		fyne.NewMenuItemSeparator(),
		a.menuItem("关闭所有窗口", shortcut(fyne.KeyW, mod|fyne.KeyModifierShift), func() { a.closeAllPanels() }),
		fyne.NewMenuItemSeparator(),
		a.menuItem("在终端中打开…", shortcut(fyne.KeyT, mod|fyne.KeyModifierShift), func() { a.openTerminal() }),
	)

	help := fyne.NewMenu("帮助",
		fyne.NewMenuItem("快捷键", func() { a.showShortcutsHelp() }),
	)

	return fyne.NewMainMenu(file, winMenu, help)
}

// menuItem 构造带快捷键的菜单项（Shortcut 同时用于显示与按键触发）。
func (a *App) menuItem(label string, sc fyne.Shortcut, action func()) *fyne.MenuItem {
	item := fyne.NewMenuItem(label, action)
	item.Shortcut = sc
	return item
}

// ---------- 文件操作（菜单/快捷键入口，作用于最近打开的文件） ----------

func (a *App) copyLastPath() {
	if a.lastFile != "" {
		a.fyneApp.Clipboard().SetContent(a.lastFile)
	}
}

func (a *App) revealLast() {
	if a.lastFile != "" {
		_ = revealInFileManager(a.lastFile)
	}
}

func (a *App) openLast() {
	if a.lastFile != "" {
		_ = openWithDefaultApp(a.lastFile)
	}
}

// showShortcutsHelp 弹出快捷键说明。
func (a *App) showShortcutsHelp() {
	text := "添加目录            ⌘/Ctrl+O\n" +
		"复制路径            ⌘/Ctrl+⇧C\n" +
		"在文件管理器中显示  ⌘/Ctrl+⇧R\n" +
		"系统打开            ⌘/Ctrl+⇧O\n" +
		"重新打开上次文件    ⌘/Ctrl+1\n" +
		"新建终端            ⌘/Ctrl+2\n" +
		"分割窗口            ⌘/Ctrl+\\\n" +
		"合并窗口            ⌘/Ctrl+⇧\\\n" +
		"关闭所有窗口        ⌘/Ctrl+⇧W\n" +
		"在终端中打开        ⌘/Ctrl+⇧T\n" +
		"退出                ⌘/Ctrl+Q"
	dialog.ShowInformation("快捷键", text, a.win)
}

// addTermPanel 新建一个内嵌终端窗口（当前工作空间根目录），并设为激活。
func (a *App) addTermPanel() {
	if a.cur == nil {
		dialog.ShowInformation("没有工作空间", "先在「文件」菜单添加目录。", a.win)
		return
	}
	tp := newTermPanel()
	var p *panel
	p = newPanel(panelTerm, tp.stack, func() { a.closePanel(p) })
	p.term = tp
	p.path = a.cur.RootPath
	p.setTitle("终端 · " + filepath.Base(a.cur.RootPath))
	p.setFooter(a.cur.RootPath)
	a.panels = append(a.panels, p)
	a.active = p
	a.refreshPanels()
	tp.start(a.cur.RootPath)
}

// findFilePanel 返回已打开的同路径文件窗口；没有则返回 nil。
func (a *App) findFilePanel(path string) *panel {
	for _, p := range a.panels {
		if p.kind == panelFile && p.path == path {
			return p
		}
	}
	return nil
}

// addFilePanel 打开文件查看器窗口（点击一个加载一个）。
// 同一文件已打开时不重复开新窗口（去重），改为激活已有窗口。
func (a *App) addFilePanel(path string) {
	if p := a.findFilePanel(path); p != nil {
		a.activatePanel(p)
		return
	}
	a.openFilePanel(path)
}

// openFilePanel 新建一个文件查看器窗口并加载文件（不查重，供内部复用），
// 并设为激活。
func (a *App) openFilePanel(path string) {
	a.lastFile = path

	grid := widget.NewTextGrid()
	grid.Scroll = fyne.ScrollBoth // 长文件在固定窗口内滚动查看，不撑大窗口
	grid.ShowLineNumbers = true
	// 平铺区被 hiddenScrollTheme 包住会隐藏滚动条；文件查看器需要自己的
	// 竖向滚动条，这里用默认主题重新包一层恢复显示。
	content := container.NewThemeOverride(grid, newCataTheme())

	var p *panel
	p = newPanel(panelFile, content, func() { a.closePanel(p) })
	p.path = path

	fc, err := a.ReadFile(path)
	switch {
	case err != nil:
		p.setTitle(filepath.Base(path))
		p.setFooter(path + "  —  " + err.Error())
		grid.SetText("读取失败: " + err.Error())
	case fc.Binary:
		p.setTitle(filepath.Base(path))
		p.setFooter(path)
		grid.SetText("（二进制文件，不支持文本预览）")
	default:
		txt := fc.Content
		if fc.Truncated {
			txt += "\n…（已截断，仅前 512KB）"
		}
		p.setTitle(fileHeader(filepath.Base(path), fc.Size))
		p.setFooter(path)
		grid.SetText(txt)
	}

	a.panels = append(a.panels, p)
	a.active = p
	a.refreshPanels()
}

// reopenLastFile 在「窗口」菜单/⌘+1：把上次打开的文件再开一个新窗口。
func (a *App) reopenLastFile() {
	if a.lastFile == "" {
		dialog.ShowInformation("没有文件", "先在左侧工作空间树点击一个文件。", a.win)
		return
	}
	a.addFilePanel(a.lastFile) // 已打开则不再重复
}

// closePanel 关闭一个窗口：终端窗口会先结束 shell；若关闭的是激活窗口，
// 自动激活相邻窗口（优先右侧，其次左侧）。然后按当前模式重排/重建。
func (a *App) closePanel(p *panel) {
	if p.term != nil {
		p.term.stop()
	}
	idx := -1
	for i, q := range a.panels {
		if q == p {
			idx = i
			a.panels = append(a.panels[:i], a.panels[i+1:]...)
			break
		}
	}
	if idx < 0 {
		return
	}
	if a.active == p {
		switch {
		case idx < len(a.panels):
			a.active = a.panels[idx] // 优先右侧窗口
		case len(a.panels) > 0:
			a.active = a.panels[len(a.panels)-1] // 否则最右侧
		default:
			a.active = nil
		}
	}
	a.refreshPanels()
}

// closeAllPanels 关闭全部窗口（退出前结束所有 shell）。
func (a *App) closeAllPanels() {
	for _, p := range a.panels {
		if p.term != nil {
			p.term.stop()
		}
	}
	a.panels = nil
	a.active = nil
	a.refreshPanels()
}

// ---------- 工作空间 ----------

func (a *App) refreshWorkspaces() {
	list, err := a.ListWorkspaces()
	if err != nil {
		dialog.ShowError(err, a.win)
		return
	}
	a.workspaces = list
	a.tree.Refresh()

	if len(list) == 0 {
		a.cur = nil
		a.tree.UnselectAll()
		return
	}

	// 尽量保持当前工作空间选中；否则选第一个。
	if a.cur != nil {
		for i := range list {
			if list[i].RootPath == a.cur.RootPath {
				a.tree.Select(a.wsKey(&list[i]))
				return
			}
		}
	}
	a.tree.Select(a.wsKey(&list[0]))
}

func (a *App) setWorkspace(ws *WorkspaceInfo) {
	if a.cur != nil && a.cur.RootPath == ws.RootPath {
		return
	}
	a.cur = ws
	a.dirCache = map[string][]FileEntry{}
	a.dirLoaded = map[string]bool{}
	a.isDirCache = map[string]bool{}
	a.tree.Refresh()
	// 已打开的文件/终端窗口保持不动（它们是独立窗口）；新窗口基于新工作空间打开。
}

// wsKey 工作空间节点的树 UID。
func (a *App) wsKey(ws *WorkspaceInfo) widget.TreeNodeID {
	return wsPrefix + ws.RootPath
}

// wsForUID 根据树节点 UID 找到所属工作空间；文件路径取包含它的最深层工作空间。
func (a *App) wsForUID(uid widget.TreeNodeID) *WorkspaceInfo {
	if strings.HasPrefix(uid, wsPrefix) {
		for i := range a.workspaces {
			if a.wsKey(&a.workspaces[i]) == uid {
				return &a.workspaces[i]
			}
		}
		return nil
	}
	var best *WorkspaceInfo
	for i := range a.workspaces {
		if isUnder(a.workspaces[i].RootPath, uid) &&
			(best == nil || len(a.workspaces[i].RootPath) > len(best.RootPath)) {
			best = &a.workspaces[i]
		}
	}
	return best
}

// addWorkspaceDialog 弹出系统原生目录选择器，选择后加入工作空间。
//
// pickFolder 会阻塞等待系统对话框，必须在 goroutine 里跑；回调里更新
// UI 用 fyne.Do 切回主线程（已声明 fyneDo 迁移）。
func (a *App) addWorkspaceDialog() {
	go func() {
		path := pickFolder()
		fyne.Do(func() {
			if path == "" {
				dialog.ShowInformation("添加目录", "已取消。", a.win)
				return
			}
			if err := a.AddWorkspace(path); err != nil {
				dialog.ShowError(err, a.win)
				return
			}
			a.refreshWorkspaces()
		})
	}()
}

func (a *App) openTerminal() {
	if a.cur == nil {
		dialog.ShowInformation("没有工作空间", "先在「文件」菜单添加目录。", a.win)
		return
	}
	if err := a.OpenTerminal(a.cur.RootPath); err != nil {
		dialog.ShowError(err, a.win)
	}
}

// ---------- 工作空间树 ----------

func (a *App) treeChildren(uid widget.TreeNodeID) []widget.TreeNodeID {
	if uid == "" {
		// 顶级：所有工作空间（顶级文件夹）。
		keys := make([]widget.TreeNodeID, 0, len(a.workspaces))
		for i := range a.workspaces {
			keys = append(keys, a.wsKey(&a.workspaces[i]))
		}
		return keys
	}
	if strings.HasPrefix(uid, wsPrefix) {
		ws := a.wsForUID(uid)
		if ws == nil {
			return nil
		}
		entries, ok := a.loadDir(ws.RootPath)
		if !ok {
			return nil
		}
		return entryPaths(entries)
	}
	if a.cur == nil {
		return nil
	}
	entries, ok := a.loadDir(uid)
	if !ok {
		return nil
	}
	return entryPaths(entries)
}

func entryPaths(entries []FileEntry) []widget.TreeNodeID {
	out := make([]widget.TreeNodeID, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Path)
	}
	return out
}

func (a *App) treeBranch(uid widget.TreeNodeID) bool {
	if uid == "" || strings.HasPrefix(uid, wsPrefix) {
		return true
	}
	if v, ok := a.isDirCache[uid]; ok {
		return v
	}
	st, err := os.Stat(uid)
	isDir := err == nil && st.IsDir()
	a.isDirCache[uid] = isDir
	return isDir
}

// onWorkspaceTapped 点击工作空间节点：设为当前工作空间并展开/收起。
func (a *App) onWorkspaceTapped(uid widget.TreeNodeID) {
	if ws := a.wsForUID(uid); ws != nil {
		a.setWorkspace(ws)
	}
	a.tree.ToggleBranch(uid)
}

func (a *App) onTreeSelected(uid widget.TreeNodeID) {
	if uid == "" {
		return
	}
	if ws := a.wsForUID(uid); ws != nil {
		a.setWorkspace(ws)
	}
	if strings.HasPrefix(uid, wsPrefix) {
		return // 工作空间的展开/收起由 onWorkspaceTapped 负责
	}
	if a.treeBranch(uid) {
		return
	}
	a.addFilePanel(uid)
}

func (a *App) loadDir(dir string) ([]FileEntry, bool) {
	if a.dirLoaded[dir] {
		return a.dirCache[dir], true
	}
	entries, err := a.ListDir(dir)
	if err != nil {
		a.dirLoaded[dir] = true
		a.dirCache[dir] = nil
		return nil, false
	}
	a.dirLoaded[dir] = true
	a.dirCache[dir] = entries
	for _, e := range entries {
		a.isDirCache[e.Path] = e.IsDir
	}
	return entries, true
}

// fileHeader 文件窗口标题：文件名 + 大小（完整路径在底部状态栏）。
func fileHeader(name string, size int64) string {
	if size <= 0 {
		return name
	}
	return fmt.Sprintf("%s  (%s)", name, fmtSize(size))
}

// fmtSize 人类可读大小。
func fmtSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/1024/1024)
}
