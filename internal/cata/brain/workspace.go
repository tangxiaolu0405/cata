package brain

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cata/internal/cata/clock"
)

// Workspace 脑子的一格分区：home 格（~/.cata/brain/workspaces/<id>/）存 memory/meta；
// 项目文档（persona、modes、skills）在 focus_path/.cata/。
type Workspace struct {
	ID         string
	RootPath   string // focus_path：绑定键（git 根 / yaml 目录 / cwd），用于选脑子
	Kind       WorkspaceKind
	Name       string
	ActiveMode string
}

// Dir 返回 home 脑子格目录（memory、meta、evolution_log）；项目文档见 ProjectCataRoot。
func (w *Workspace) Dir() string {
	return filepath.Join(workspacesRoot(), w.ID)
}

func (w *Workspace) metaPath() string { return filepath.Join(w.Dir(), RelMetaJSON) }

// ModeDir 返回项目 .cata 下某 mode 目录。
func (w *Workspace) ModeDir(modeID string) string {
	return filepath.Join(w.ProjectCataRoot(), DirModes, NormalizeModeID(modeID))
}

func (w *Workspace) modeID() string {
	return NormalizeModeID(w.ActiveMode)
}

// PersonaLocalPath 项目级 focus 说明（focus_path/.cata/persona.local.md）。
func (w *Workspace) PersonaLocalPath() string {
	return filepath.Join(w.ProjectCataRoot(), RelPersonaLocal)
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
		"active_mode": NormalizeModeID(w.ActiveMode),
		"updated_at":  clock.RFC3339(),
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(w.metaPath(), data, 0644)
}

// EnsureScaffold 创建 home 记忆目录与项目 .cata 文档树。
func (w *Workspace) EnsureScaffold() error {
	if err := os.MkdirAll(w.Dir(), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(w.ProjectCataRoot(), 0755); err != nil {
		return err
	}
	for _, d := range []string{
		filepath.Join(w.Dir(), "memory", "short"),
		w.LongTermDir(),
		w.ArchiveDir(),
		w.ModeDir(ModeDefaultID),
		filepath.Join(w.ProjectCataRoot(), DirSkills),
	} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}

	if err := w.migrateHomeBrainDocsToProject(); err != nil {
		return err
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
	if err := ensureFile(filepath.Join(modeDir, FileCapabilities), "skills: []\nmcp: []\n"); err != nil {
		return err
	}
	if err := EnsureShortTermFileFor(w); err != nil {
		return err
	}
	if err := ensureFile(w.MemoryIndexPath(), `{"version":1,"entries":[]}`+"\n"); err != nil {
		return err
	}
	if err := MigrateLearningFragmentsFor(w); err != nil {
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

const defaultPersonaLocal = `# Focus context（项目 .cata）

> **只写仓库事实**（项目是什么、数据源、产出目录、技术栈）。身份/偏好 → modes/…/persona.md；SOP/格式 → modes/…/behavior.md。

## Project

## Tech stack

## Current snapshot

> 仅保留会变的运行态（如最新交易日、当日统计）；稳定事实放 Project。整节 replace，勿复制到 persona.md。

`

const defaultModePersona = `# Persona

> **只写身份与偏好**（自称、语气、用户偏好与禁忌）。项目事实 → persona.local.md；流水线/格式 → behavior.md。

## Who I am

## Preferences & taboos

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
			y.ActiveMode = NormalizeModeID(strings.TrimSpace(strings.TrimPrefix(line, "active_mode:")))
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
