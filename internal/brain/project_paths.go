package brain

import (
	"path/filepath"
	"strings"
)

// ProjectCataRoot 返回 focus_path/.cata（项目内 persona、modes、skills）。
func (w *Workspace) ProjectCataRoot() string {
	return filepath.Join(w.RootPath, ProjectCataDir)
}

// ProjectPath 将相对路径解析到项目 .cata 目录下。
func (w *Workspace) ProjectPath(rel string) string {
	return filepath.Join(w.ProjectCataRoot(), filepath.FromSlash(rel))
}

// IsHomeBrainRel 判断演进/chat 相对路径是否落在 home 脑子格（memory、meta、evolution_log）。
func IsHomeBrainRel(rel string) bool {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	rel = strings.TrimPrefix(rel, "brain/")
	switch {
	case rel == RelMetaJSON, rel == RelEvolutionLog:
		return true
	case strings.HasPrefix(rel, "memory/"):
		return true
	default:
		return false
	}
}

// ResolveBrainDocAbs 将 workspace 相对路径解析为磁盘绝对路径（home 格或项目 .cata）。
func ResolveBrainDocAbs(w *Workspace, rel string) (abs string, err error) {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if IsHomeBrainRel(rel) {
		return PathUnderBase(w.Dir(), filepath.FromSlash(rel))
	}
	return PathUnderBase(w.ProjectCataRoot(), filepath.FromSlash(rel))
}
