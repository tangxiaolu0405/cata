package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// showTreeMenu 在工作空间树上弹出右键菜单。
// 工作空间节点：终端 / 文件管理器 / 复制路径 /（手动添加的）从列表移除；
// 文件节点：打开 / 终端 / 文件管理器 / 复制路径 / 删除。
func (a *App) showTreeMenu(uid widget.TreeNodeID, pos fyne.Position) {
	if uid == "" {
		return
	}
	if strings.HasPrefix(uid, wsPrefix) {
		a.showWorkspaceMenu(uid, pos)
		return
	}
	st, err := os.Stat(uid)
	if err != nil {
		return
	}
	isDir := st.IsDir()
	name := filepath.Base(uid)

	m := fyne.NewMenu("文件操作",
		fyne.NewMenuItem("打开", func() { a.openNode(uid, isDir) }),
		fyne.NewMenuItem("在终端中打开", func() { a.openTerminalAt(uid, isDir) }),
		fyne.NewMenuItem("在文件管理器中显示", func() { revealInFileManager(uid) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("复制路径", func() { a.fyneApp.Clipboard().SetContent(uid) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("删除 “"+name+"”", func() { a.confirmDelete(uid, name, isDir) }),
	)
	if isDir {
		// 目录节点额外提供「刷新」：清该目录缓存，反映外部文件系统变更
		// （dirCache 无自动失效，此前外部新增/删除不会反映到树）。
		m.Items = append(m.Items, fyne.NewMenuItemSeparator(), fyne.NewMenuItem("刷新", func() { a.refreshDir(uid) }))
	}
	widget.ShowPopUpMenuAtPosition(m, a.win.Canvas(), pos)
}

// refreshDir 清除指定目录的缓存并刷新树（反映外部文件系统变更）。
func (a *App) refreshDir(dir string) {
	delete(a.dirCache, dir)
	delete(a.dirLoaded, dir)
	a.tree.Refresh()
}

// showWorkspaceMenu 工作空间节点的右键菜单。
func (a *App) showWorkspaceMenu(uid widget.TreeNodeID, pos fyne.Position) {
	ws := a.wsForUID(uid)
	if ws == nil {
		return
	}
	m := fyne.NewMenu("工作空间",
		fyne.NewMenuItem("在终端中打开", func() {
			if err := a.OpenTerminal(ws.RootPath); err != nil {
				dialog.ShowError(err, a.win)
			}
		}),
		fyne.NewMenuItem("在文件管理器中显示", func() { _ = revealInFileManager(ws.RootPath) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("复制路径", func() { a.fyneApp.Clipboard().SetContent(ws.RootPath) }),
	)
	if ws.Source == "extra" {
		m.Items = append(m.Items,
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("从列表移除", func() { a.confirmRemoveWorkspace(ws) }),
		)
	}
	widget.ShowPopUpMenuAtPosition(m, a.win.Canvas(), pos)
}

// openNode 打开节点：目录切换展开/收起，文件打开预览。
func (a *App) openNode(uid widget.TreeNodeID, isDir bool) {
	if isDir {
		a.tree.ToggleBranch(uid)
		return
	}
	a.tree.Select(uid)
}

// openTerminalAt 在节点所在目录打开系统终端。
func (a *App) openTerminalAt(uid widget.TreeNodeID, isDir bool) {
	dir := uid
	if !isDir {
		dir = filepath.Dir(uid)
	}
	if err := a.OpenTerminal(dir); err != nil {
		dialog.ShowError(err, a.win)
	}
}

// confirmDelete 二次确认后永久删除文件/目录，并刷新文件树。
func (a *App) confirmDelete(path, name string, isDir bool) {
	msg := fmt.Sprintf("确定永久删除 “%s” 吗？\n此操作不可恢复。", name)
	if isDir {
		msg = fmt.Sprintf("确定永久删除目录 “%s” 及其全部内容吗？\n此操作不可恢复。", name)
	}
	dialog.ShowConfirm("删除", msg, func(ok bool) {
		if !ok {
			return
		}
		if err := os.RemoveAll(path); err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		a.afterDelete(path)
	}, a.win)
}

// confirmRemoveWorkspace 从列表移除手动添加的工作空间（不删除磁盘文件）。
func (a *App) confirmRemoveWorkspace(ws *WorkspaceInfo) {
	dialog.ShowConfirm("移除工作空间",
		fmt.Sprintf("从列表移除 “%s”？\n不会删除磁盘上的任何文件。", ws.Name),
		func(ok bool) {
			if !ok {
				return
			}
			if err := a.RemoveWorkspace(ws.RootPath); err != nil {
				dialog.ShowError(err, a.win)
				return
			}
			a.refreshWorkspaces()
		}, a.win)
}

// afterDelete 删除后刷新树与内容区。
func (a *App) afterDelete(path string) {
	parent := filepath.Dir(path)
	if a.cur != nil && isUnder(a.cur.RootPath, parent) {
		delete(a.dirCache, parent)
		delete(a.dirLoaded, parent)
		delete(a.isDirCache, path)
		a.tree.Refresh()
	}
}

func isUnder(root, p string) bool {
	root = filepath.Clean(root)
	p = filepath.Clean(p)
	if p == root {
		return true
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// revealInFileManager 在系统文件管理器中显示该路径（macOS 会选中文件）。
func revealInFileManager(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-R", path)
	case "windows":
		cmd = exec.Command("explorer", "/select,"+path)
	default:
		dir := path
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			dir = filepath.Dir(path)
		}
		cmd = exec.Command("xdg-open", dir)
	}
	return cmd.Start()
}

// openWithDefaultApp 用系统默认应用打开路径（文件或目录）。
func openWithDefaultApp(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}
