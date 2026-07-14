package brain

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// NormalizeModeID 规范化 mode 目录名；空或 "default"（LLM/配置常见笔误）→ "_default"。
func NormalizeModeID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || strings.EqualFold(id, "default") {
		return ModeDefaultID
	}
	return id
}

// normalizeModePathRel 将 updates 路径中的 modes/default/ 纠正为 modes/_default/。
func normalizeModePathRel(rel string) string {
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")
	if len(parts) >= 2 && parts[0] == DirModes && strings.EqualFold(parts[1], "default") {
		parts[1] = ModeDefaultID
		return strings.Join(parts, "/")
	}
	return rel
}

// migrateDefaultModeAlias 合并误建的 modes/default/ 到 modes/_default/ 并删除前者。
func (w *Workspace) migrateDefaultModeAlias() error {
	wrong := filepath.Join(w.ProjectCataRoot(), DirModes, "default")
	right := w.ModeDir(ModeDefaultID)
	st, err := os.Stat(wrong)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !st.IsDir() {
		return nil
	}
	if err := os.MkdirAll(right, 0755); err != nil {
		return err
	}
	if err := mergeDirFiles(wrong, right); err != nil {
		return err
	}
	return os.RemoveAll(wrong)
}

func mergeDirFiles(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if _, err := os.Stat(target); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
