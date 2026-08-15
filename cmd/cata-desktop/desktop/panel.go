package desktop

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// panelKind 窗口类型。
type panelKind int

const (
	panelFile panelKind = iota // 文件查看器窗口
	panelTerm                  // 内嵌终端窗口
)

// titleBarWidth 定长标题栏的总宽度：标题 + ✕ 组成一块固定宽度的标题栏，
// 不随面板宽度铺满。同时作为窗口的最小宽度（tileLayout.minW）。
const titleBarWidth = 200

// titleBarHeight 标题栏高度。
const titleBarHeight = 36

// panel 一个可并排的窗口：定长标题栏 + 内容区 + 底部路径栏。
// 文件查看器内容为 TextGrid；终端内容为 termPanel 的 stack。
//
// 两种呈现（参考 VSCode）：
//   - Tab 模式（默认）：所有窗口标题连成一行标签栏（tabbar.go），
//     激活窗口的 content 占据全部空间；此时 box 为 nil，content 在
//     tab 内容区里。
//   - Split 模式（分割）：每个窗口 buildBox() 构建外壳（定长标题栏 +
//     空隙 + content + footer + 右分隔线），由 tileLayout 并排。
//
// 同一份 content 在两种模式间移动，保证同一时刻只挂在一棵对象树上。
type panel struct {
	kind    panelKind
	box     *fyne.Container // split 模式外壳（buildBox 构建；tab 模式为 nil）
	title   *widget.Label
	footer  *widget.Label // 底部状态栏：文件完整路径 / 终端启动目录
	content fyne.CanvasObject
	path    string     // 文件路径 或 终端启动目录
	term    *termPanel // 仅终端窗口
	onClose func()     // 关闭回调（split 外壳的 ✕ 使用）
}

// newPanel 构造统一窗口：只创建标题/路径标签与内容，不建外壳。
// 外壳（split 模式）由 buildBox 按需构建，避免 content 同时挂在多个树里。
func newPanel(kind panelKind, content fyne.CanvasObject, onClose func()) *panel {
	title := widget.NewLabel("")
	title.Truncation = fyne.TextTruncateEllipsis // 长路径在定长块内省略号截断
	footer := widget.NewLabel("")
	footer.Truncation = fyne.TextTruncateEllipsis
	footer.Importance = widget.LowImportance
	return &panel{kind: kind, content: content, onClose: onClose, title: title, footer: footer}
}

// buildBox 构建 split 模式外壳：
//
// 标题栏 = 一块固定总宽度的标题块（titleBarWidth，默认 200px）：
//   - 背景条只铺满这块定长区域，不随面板宽度拉伸；
//   - 左侧标题省略号截断，在剩余宽度内显示；
//   - 右侧固定 28×28 ✕（GridWrap 固定尺寸，标题长短变化不会让它位移或变形）。
//
// 标题栏正下方预留 scrollbarRowH 高的空隙：hbarOverlayLayout 把水平滚动条
// 叠放在这里（多个窗口横向滑动时，滚动条固定贴在标题行下方，不遮挡内容）。
//
// 底部路径栏：文件完整路径 / 终端启动目录单独放在这里，标题只放文件名。
func (p *panel) buildBox() {
	closeBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), p.onClose)
	closeBtn.Importance = widget.LowImportance
	btn := container.NewGridWrap(fyne.NewSize(28, 28), closeBtn) // 固定定长 ✕

	// 定长标题块：背景矩形铺满 titleBarWidth，Border 负责标题 + ✕ 的左右分布。
	bar := container.NewStack(
		canvas.NewRectangle(theme.Color(theme.ColorNameInputBackground)), // 只铺定长块，不铺满面板
		container.NewBorder(nil, nil, nil, btn, p.title),
	)
	barFixed := container.NewGridWrap(fyne.NewSize(titleBarWidth, titleBarHeight), bar) // 固定总宽度

	// 标题栏正下方的滚动条空隙（透明）：给叠放的 hscrollBar 让位。
	gap := canvas.NewRectangle(color.Transparent)
	gapWrap := container.NewGridWrap(fyne.NewSize(titleBarWidth, scrollbarRowH), gap)
	body := container.NewBorder(gapWrap, nil, nil, nil, p.content)

	// 底部路径栏：1px 顶分隔线 + 灰色路径文本（完整路径单独放这里）。
	footLine := canvas.NewRectangle(theme.Color(theme.ColorNameSeparator))
	footer := container.NewBorder(footLine, nil, nil, nil, p.footer)

	sep := canvas.NewRectangle(theme.Color(theme.ColorNameSeparator)) // 1px 右分隔线
	p.box = container.NewBorder(barFixed, footer, nil, sep, body)     // sep 在右：相邻窗口之间的 1px 边界
}

// setTitle 设置标题并刷新（标题块定长，超出部分省略号截断）。
func (p *panel) setTitle(text string) {
	p.title.SetText(text)
	if p.box != nil {
		p.box.Refresh()
	}
}

// setFooter 设置底部状态栏文本（文件完整路径 / 终端启动目录）。
func (p *panel) setFooter(text string) {
	p.footer.SetText(text)
}
