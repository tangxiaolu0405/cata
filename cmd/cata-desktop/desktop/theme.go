package desktop

import (
	_ "embed"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

//go:embed assets/fonts/NotoSansCJKsc-Regular.otf
var fontData []byte

// cataTheme 基于默认主题，但把普通文本字体换成捆绑的中文字体（Noto Sans CJK SC），
// 保证界面中文正常显示、跨平台不依赖系统字体。等宽（Monospace）字体保持
// 默认等宽字体：终端与代码预览需要真正的等宽度量，CJK 字体会破坏对齐
// （中文仍通过 Fyne 的系统字体回退渲染）。
type cataTheme struct {
	fyne.Theme
}

func newCataTheme() fyne.Theme {
	return &cataTheme{Theme: theme.DefaultTheme()}
}

// Font 普通/加粗/斜体统一用中文字体；等宽样式用默认等宽字体。
func (t *cataTheme) Font(style fyne.TextStyle) fyne.Resource {
	if style.Monospace {
		return theme.DefaultTextMonospaceFont()
	}
	return fyne.NewStaticResource("NotoSansCJKsc-Regular.otf", fontData)
}
