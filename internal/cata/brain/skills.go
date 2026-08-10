package brain

import (
	"os"
	"path/filepath"
	"strings"
)

// SkillDir 返回项目 .cata 内某 skill 的目录。
func (w *Workspace) SkillDir(skillID string) string {
	return filepath.Join(w.ProjectCataRoot(), DirSkills, strings.TrimSpace(skillID))
}

// SkillMarkdownPath workspace 脑子内 SKILL.md。
func (w *Workspace) SkillMarkdownPath(skillID string) string {
	return filepath.Join(w.SkillDir(skillID), FileSkillMD)
}

// SkillManifestPath workspace 脑子内 manifest.yaml。
func (w *Workspace) SkillManifestPath(skillID string) string {
	return filepath.Join(w.SkillDir(skillID), FileSkillManifest)
}

// ListWorkspaceSkillIDs 扫描 skills/*/manifest.yaml。
func ListWorkspaceSkillIDs(w *Workspace) ([]string, error) {
	if w == nil {
		return nil, nil
	}
	root := filepath.Join(w.ProjectCataRoot(), DirSkills)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		if _, err := os.Stat(filepath.Join(root, id, FileSkillManifest)); err == nil {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// GlobalSkillMarkdownPath ~/.cata/skills/<name>/SKILL.md
func GlobalSkillMarkdownPath(name string) string {
	return filepath.Join(CataHome(), DirSkills, strings.TrimSpace(name), FileSkillMD)
}
