package brain

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cata/internal/clock"
)

// Workspace 脑子的一格分区（~/.cata/brain/workspaces/<id>/），由 focus_path 选中，不是产出区。
type Workspace struct {
	ID         string
	RootPath   string // focus_path：绑定键（git 根 / yaml 目录 / cwd），用于选脑子
	Kind       WorkspaceKind
	Name       string
	ActiveMode string
}

// Dir 返回该格脑子在 CATA_HOME 下的目录。
func (w *Workspace) Dir() string {
	return filepath.Join(workspacesRoot(), w.ID)
}

func (w *Workspace) metaPath() string { return filepath.Join(w.Dir(), RelMetaJSON) }

// ModeDir 返回某 mode 目录。
func (w *Workspace) ModeDir(modeID string) string {
	if strings.TrimSpace(modeID) == "" {
		modeID = ModeDefaultID
	}
	return filepath.Join(w.Dir(), DirModes, modeID)
}

func (w *Workspace) modeID() string {
	if w.ActiveMode != "" {
		return w.ActiveMode
	}
	return ModeDefaultID
}

// PersonaLocalPath workspace 级项目说明。
func (w *Workspace) PersonaLocalPath() string {
	return filepath.Join(w.Dir(), RelPersonaLocal)
}

// PersonaPath 当前 mode 的 persona（≈ 原 hot.md）。
func (w *Workspace) PersonaPath() string {
	return filepath.Join(w.ModeDir(w.modeID()), FilePersona)
}

// ShortTermPath 短期记忆。
func (w *Workspace) ShortTermPath() string {
	return filepath.Join(w.Dir(), RelShortCurrent)
}

// LongTermDir 长期记忆目录。
func (w *Workspace) LongTermDir() string {
	return filepath.Join(w.Dir(), RelMemoryLong)
}

// ArchiveDir 档案目录。
func (w *Workspace) ArchiveDir() string {
	return filepath.Join(w.Dir(), RelMemoryArchive)
}

// EvolutionLogPath 本工作区演进日志。
func (w *Workspace) EvolutionLogPath() string {
	return filepath.Join(w.Dir(), RelEvolutionLog)
}

// MemoryIndexPath 记忆索引（按需加载）。
func (w *Workspace) MemoryIndexPath() string {
	return filepath.Join(w.Dir(), RelMemoryIndex)
}

// Path 工作区内的相对路径。
func (w *Workspace) Path(rel string) string {
	return filepath.Join(w.Dir(), filepath.FromSlash(rel))
}

func (w *Workspace) saveMeta() error {
	m := map[string]string{
		"id":          w.ID,
		"root_path":   w.RootPath,
		"kind":        string(w.Kind),
		"name":        w.Name,
		"active_mode": w.ActiveMode,
		"updated_at":  clock.RFC3339(),
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(w.metaPath(), data, 0644)
}

// EnsureScaffold 创建 workspace 目录树与 _default mode。
func (w *Workspace) EnsureScaffold() error {
	if err := os.MkdirAll(w.Dir(), 0755); err != nil {
		return err
	}
	dirs := []string{
		filepath.Join(w.Dir(), "memory", "short"),
		w.LongTermDir(),
		w.ArchiveDir(),
		w.ModeDir(ModeDefaultID),
		filepath.Join(w.Dir(), DirSkills),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}

	if err := w.saveMeta(); err != nil {
		return err
	}

	if err := ensureFile(w.PersonaLocalPath(), defaultPersonaLocal); err != nil {
		return err
	}
	modeDir := w.ModeDir(ModeDefaultID)
	if err := ensureFile(filepath.Join(modeDir, FilePersona), defaultModePersona); err != nil {
		return err
	}
	if err := ensureFile(filepath.Join(modeDir, FileBehavior), "# Mode behavior\n\n(Inherit global behavior; override here if needed.)\n"); err != nil {
		return err
	}
	if err := ensureFile(filepath.Join(modeDir, FileConstraints), "# Mode constraints\n\n"); err != nil {
		return err
	}
	if err := ensureFile(filepath.Join(modeDir, FileCapabilities), "skills: []\nmcp:\n  - browser\n"); err != nil {
		return err
	}
	if err := EnsureShortTermFileFor(w); err != nil {
		return err
	}
	if err := ensureFile(w.MemoryIndexPath(), `{"version":1,"entries":[]}`+"\n"); err != nil {
		return err
	}
	return writeProjectLink(w)
}

func ensureFile(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0644)
}

const defaultPersonaLocal = `# Focus context（在脑子里）

> 对 focus_path 所指对象（常为 git 根）的说明；**不是**产出区 cwd 的全文镜像。

## Current focus

`

const defaultModePersona = `# Persona

> Identity and preferences for this mode (maintained by autonomous evolution from short-term memory).

## Who I am

## Current goals

## Preferences

`

// projectWorkspaceYAML 解析项目内 .cata/workspace.yaml（可选）。
type projectWorkspaceYAML struct {
	Name       string `yaml:"name"`
	ActiveMode string `yaml:"active_mode"`
}

func readProjectWorkspaceYAML(root string) projectWorkspaceYAML {
	p := filepath.Join(root, ProjectCataDir, FileWorkspaceYAML)
	data, err := os.ReadFile(p)
	if err != nil {
		return projectWorkspaceYAML{}
	}
	var y projectWorkspaceYAML
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name:") {
			y.Name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		}
		if strings.HasPrefix(line, "active_mode:") {
			y.ActiveMode = strings.TrimSpace(strings.TrimPrefix(line, "active_mode:"))
		}
	}
	return y
}

func writeProjectLink(w *Workspace) error {
	if w.Kind == KindEphemeral {
		return nil
	}
	dir := filepath.Join(w.RootPath, ProjectCataDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	body := fmt.Sprintf("id: %s\n", w.ID)
	linkPath := filepath.Join(dir, FileWorkspaceLink)
	if _, err := os.Stat(linkPath); err == nil {
		return nil
	}
	return os.WriteFile(linkPath, []byte(body), 0644)
}
