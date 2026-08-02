package brain

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ModeInfo 项目 .cata/modes 下一格角色卡摘要。
type ModeInfo struct {
	ID       string
	OneLiner string
	HasDir   bool
}

// ListProjectModes 列出 focus_path/.cata/modes/*。
func ListProjectModes(w *Workspace) ([]ModeInfo, error) {
	if w == nil {
		return nil, fmt.Errorf("no workspace")
	}
	root := filepath.Join(w.ProjectCataRoot(), DirModes)
	ents, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []ModeInfo
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		id := e.Name()
		one := firstPersonaLine(filepath.Join(root, id, FilePersona))
		out = append(out, ModeInfo{ID: id, OneLiner: one, HasDir: true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func firstPersonaLine(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ">") {
			continue
		}
		if len(line) > 120 {
			return line[:120] + "…"
		}
		return line
	}
	return ""
}

// ModeExists 目录是否存在（modeID 经 ResolveDelegateModeID）。
func ModeExists(w *Workspace, modeID string) bool {
	if w == nil {
		return false
	}
	id := ResolveDelegateModeID(modeID)
	st, err := os.Stat(w.ModeDir(id))
	return err == nil && st.IsDir()
}

// ResolveDelegateModeID 解析委托目标：别名归一到 _default；专职 id 原样保留。
func ResolveDelegateModeID(modeID string) string {
	return NormalizeModeID(modeID)
}

// LoadModePromptBundle 加载某 mode 的 persona/behavior/constraints（有界）。
func LoadModePromptBundle(w *Workspace, modeID string, maxPerFile int) (string, error) {
	if w == nil {
		return "", fmt.Errorf("no workspace")
	}
	id := ResolveDelegateModeID(modeID)
	if maxPerFile <= 0 {
		maxPerFile = 4000
	}
	dir := w.ModeDir(id)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("mode %q not found under %s", id, filepath.Join(w.ProjectCataRoot(), DirModes))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Mode role card (`%s`)\n\n", id)
	for _, f := range []string{FilePersona, FileBehavior, FileConstraints} {
		p := filepath.Join(dir, f)
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		s := strings.TrimSpace(string(data))
		if s == "" {
			continue
		}
		if len(s) > maxPerFile {
			s = s[:maxPerFile] + "\n…(truncated)"
		}
		fmt.Fprintf(&b, "### %s\n\n%s\n\n", f, s)
	}
	return strings.TrimSpace(b.String()), nil
}

// FormatModesList 给工具/主 agent 看的列表文本。
func FormatModesList(modes []ModeInfo) string {
	if len(modes) == 0 {
		return "(no modes under .cata/modes/)"
	}
	var b strings.Builder
	b.WriteString("modes:\n")
	for _, m := range modes {
		line := m.OneLiner
		if line == "" {
			line = "(no persona one-liner)"
		}
		fmt.Fprintf(&b, "- %s: %s\n", m.ID, line)
	}
	return strings.TrimRight(b.String(), "\n")
}
