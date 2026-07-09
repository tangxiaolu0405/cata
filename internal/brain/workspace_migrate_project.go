package brain

import (
	"os"
	"path/filepath"
)

// migrateHomeBrainDocsToProject 将旧版 home 脑子格内的 persona/modes/skills 迁入 focus_path/.cata/。
func (w *Workspace) migrateHomeBrainDocsToProject() error {
	home := w.Dir()
	proj := w.ProjectCataRoot()
	for _, name := range []string{RelPersonaLocal, DirModes, DirSkills} {
		src := filepath.Join(home, name)
		dst := filepath.Join(proj, name)
		st, err := os.Stat(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if st.IsDir() {
			if err := os.MkdirAll(dst, 0755); err != nil {
				return err
			}
			if err := mergeDirFiles(src, dst); err != nil {
				return err
			}
			continue
		}
		if _, err := os.Stat(dst); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return err
		}
		if err := copyFile(src, dst); err != nil {
			return err
		}
	}
	return w.migrateDefaultModeAlias()
}
