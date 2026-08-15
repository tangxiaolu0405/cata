package desktop

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// nodeCell 工作空间树节点内容：
//   - 工作空间节点：两行（名称加粗 + 路径灰色斜体），点击展开/收起并设为当前工作空间；
//   - 文件/目录节点：单行（省略号截断），点击选中。
//
// 同时实现 Tapped / TappedSecondary：Fyne 事件分发会把最上层命中对象当作
// 目标，若不实现 Tapped 会吃掉树节点的单击事件，所以这里显式补上。
type nodeCell struct {
	widget.BaseWidget
	a    *App
	uid  widget.TreeNodeID
	name *widget.Label
	path *widget.Label
}

func newNodeCell(a *App) *nodeCell {
	n := &nodeCell{
		a:    a,
		name: widget.NewLabel(""),
		path: widget.NewLabel(""),
	}
	n.name.Truncation = fyne.TextTruncateEllipsis
	n.path.Truncation = fyne.TextTruncateEllipsis
	n.path.TextStyle = fyne.TextStyle{Italic: true}
	n.path.Importance = widget.LowImportance
	n.ExtendBaseWidget(n)
	return n
}

func (n *nodeCell) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewVBox(n.name, n.path))
}

// Tapped 单击：工作空间节点展开/收起并切换当前工作空间；其余节点走选中。
func (n *nodeCell) Tapped(_ *fyne.PointEvent) {
	if strings.HasPrefix(n.uid, wsPrefix) {
		n.a.onWorkspaceTapped(n.uid)
		return
	}
	n.a.tree.Select(n.uid)
}

// TappedSecondary 右键：弹出节点操作菜单。
func (n *nodeCell) TappedSecondary(ev *fyne.PointEvent) {
	n.a.showTreeMenu(n.uid, ev.AbsolutePosition)
}
