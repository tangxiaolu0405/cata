package brain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cata/internal/cata/config"
)

// PathUnderBase resolves rel under base and rejects path traversal.
func PathUnderBase(base, rel string) (string, error) {
	if base == "" {
		return "", fmt.Errorf("base directory not configured")
	}
	rel = filepath.Clean(strings.TrimSpace(rel))
	if rel == "." {
		return "", fmt.Errorf("path required")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute path not allowed")
	}
	full := filepath.Join(base, rel)
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	fullAbs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	r, err := filepath.Rel(baseAbs, fullAbs)
	if err != nil || strings.HasPrefix(r, "..") {
		return "", fmt.Errorf("path escapes allowed directory")
	}
	return fullAbs, nil
}

// ExecWorkingDir returns run_command / run_skill cwd under the output area.
func ExecWorkingDir() (string, error) {
	return ExecWorkingDirFor(OutputCwd())
}

// ExecWorkingDirFor 显式指定产出区的 run_command / run_skill cwd（多 chat 并行勿依赖全局 OutputCwd）。
func ExecWorkingDirFor(outCwd string) (string, error) {
	if config.Config == nil {
		return "", fmt.Errorf("config not loaded")
	}
	base := outCwd
	if base == "" {
		base = config.GetBrainBaseDir()
	}
	sub := strings.TrimSpace(config.Config.Exec.WorkingDir)
	if sub == "" {
		return filepath.Abs(base)
	}
	d, err := PathUnderBase(base, filepath.Clean(sub))
	if err != nil {
		return "", err
	}
	st, err := os.Stat(d)
	if err != nil {
		return "", fmt.Errorf("exec.working_dir: %w", err)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("exec.working_dir must be an existing directory under brain.base_dir: %s", sub)
	}
	return d, nil
}
