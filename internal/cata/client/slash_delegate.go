package client

import (
	"fmt"
	"io"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// slashCmdDelegate 单行展示：/command  description（描述在命令右侧）。
type slashCmdDelegate struct {
	styles list.DefaultItemStyles
}

func newSlashCmdDelegate() slashCmdDelegate {
	return slashCmdDelegate{styles: list.NewDefaultItemStyles()}
}

func (d slashCmdDelegate) Height() int                         { return 1 }
func (d slashCmdDelegate) Spacing() int                        { return 0 }
func (d slashCmdDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (d slashCmdDelegate) ShortHelp() []key.Binding            { return nil }
func (d slashCmdDelegate) FullHelp() [][]key.Binding           { return nil }

func (d slashCmdDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	pi, ok := item.(pickItem)
	if !ok || m.Width() <= 0 {
		return
	}
	cmd := pi.Title()
	desc := pi.Description()
	pad := d.styles.NormalTitle.GetPaddingLeft() + d.styles.NormalTitle.GetPaddingRight()
	textW := m.Width() - pad
	if textW < 8 {
		textW = m.Width()
	}

	isSelected := index == m.Index()
	if isSelected && m.FilterState() != list.Filtering {
		cmd = d.styles.SelectedTitle.Render(ansi.Truncate(cmd, textW, "…"))
		desc = d.styles.SelectedDesc.Render(desc)
	} else {
		cmd = d.styles.NormalTitle.Render(ansi.Truncate(cmd, textW/2, "…"))
		desc = d.styles.NormalDesc.Render(desc)
	}
	fmt.Fprintf(w, "%s  %s", cmd, desc) //nolint:errcheck
}
