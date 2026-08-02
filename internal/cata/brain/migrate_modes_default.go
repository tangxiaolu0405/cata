package brain

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 把短暂误用的 modes/_default 幂等收回 modes/_default。
const migratedModesDefaultV2 = ".migrated_modes_default_v2"
const legacyMigratedModesOrchestratorV1 = ".migrated_modes_orchestrator_v1"

// maybeMigrateModesDefaultV2 在 EnsureScaffold 中调用。
func (w *Workspace) maybeMigrateModesDefaultV2() error {
	return w.migrateModesDefaultV2()
}

// migrateModesDefaultV2 执行一次幂等回迁（单测可直接调用）。
func (w *Workspace) migrateModesDefaultV2() error {
	root := w.ProjectCataRoot()
	if root == "" {
		return nil
	}
	marker := filepath.Join(root, migratedModesDefaultV2)
	if _, err := os.Stat(marker); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := w.migrateDefaultModeAlias(); err != nil {
		return err
	}

	orchDir := filepath.Join(root, DirModes, ModeAliasOrchestratorID)
	defDir := w.ModeDir(ModeDefaultID)

	orchExists := dirExists(orchDir)
	defExists := dirExists(defDir)

	switch {
	case orchExists && !defExists:
		if err := os.Rename(orchDir, defDir); err != nil {
			if err := os.MkdirAll(defDir, 0755); err != nil {
				return err
			}
			if err := mergeDirFiles(orchDir, defDir); err != nil {
				return err
			}
			_ = os.RemoveAll(orchDir)
		}
	case orchExists && defExists:
		if err := mergeDirFiles(orchDir, defDir); err != nil {
			return err
		}
		_ = os.RemoveAll(orchDir)
	}

	if err := w.rewriteActiveModeToDefault(); err != nil {
		return err
	}

	_ = os.Remove(filepath.Join(root, legacyMigratedModesOrchestratorV1))
	return os.WriteFile(marker, []byte("v2\n"), 0644)
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func (w *Workspace) rewriteActiveModeToDefault() error {
	cur := strings.TrimSpace(w.ActiveMode)
	if cur == "" || strings.EqualFold(cur, "default") || cur == ModeAliasOrchestratorID || NormalizeModeID(cur) == ModeDefaultID {
		w.ActiveMode = ModeDefaultID
	}
	if err := w.saveMeta(); err != nil {
		return err
	}
	yamlPath := filepath.Join(w.ProjectCataRoot(), FileWorkspaceYAML)
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	lines := strings.Split(string(data), "\n")
	changed := false
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "active_mode:") {
			val := strings.TrimSpace(strings.TrimPrefix(trim, "active_mode:"))
			val = strings.Trim(val, `"'`)
			if val == "" || strings.EqualFold(val, "default") || val == ModeAliasOrchestratorID {
				lines[i] = "active_mode: " + ModeDefaultID
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	return os.WriteFile(yamlPath, []byte(strings.Join(lines, "\n")), 0644)
}

// MigratedModesDefaultV2MarkerName 供测试使用。
func MigratedModesDefaultV2MarkerName() string { return migratedModesDefaultV2 }

// DebugMigrateModesDefaultV2 仅供测试直接触发回迁逻辑。
func DebugMigrateModesDefaultV2(w *Workspace) error {
	if w == nil {
		return fmt.Errorf("workspace nil")
	}
	return w.migrateModesDefaultV2()
}

// loadMetaActiveMode 测试辅助：读 meta.json 的 active_mode。
func loadMetaActiveMode(w *Workspace) (string, error) {
	data, err := os.ReadFile(w.metaPath())
	if err != nil {
		return "", err
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return "", err
	}
	return m["active_mode"], nil
}
