package brain

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var modeIDRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// EnsureModeDraft 创建 draft mode 种子（已存在则不覆盖非空 persona）。
func EnsureModeDraft(w *Workspace, modeID, persona, behavior string) (string, error) {
	if w == nil {
		return "", fmt.Errorf("workspace required")
	}
	id := strings.TrimSpace(modeID)
	id = strings.TrimPrefix(id, "modes/")
	if id == "" || id == ModeDefaultID || id == ModeAliasOrchestratorID || strings.EqualFold(id, "default") || !modeIDRe.MatchString(id) {
		return "", fmt.Errorf("invalid new mode id %q", modeID)
	}
	dir := w.ModeDir(id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	if strings.TrimSpace(persona) == "" {
		persona = fmt.Sprintf("# Persona — %s (draft)\n\n## Who I am\n\nDraft specialist mode crystallized by evolve.\n", id)
	}
	if strings.TrimSpace(behavior) == "" {
		behavior = fmt.Sprintf("# Mode behavior — %s\n\n1. Follow parent task and artifacts\n2. Write allowed artifacts only\n", id)
	}
	personaPath := filepath.Join(dir, FilePersona)
	if st, err := os.Stat(personaPath); err != nil || st.Size() < 40 {
		if err := os.WriteFile(personaPath, []byte(persona), 0644); err != nil {
			return "", err
		}
	}
	behPath := filepath.Join(dir, FileBehavior)
	if st, err := os.Stat(behPath); err != nil || st.Size() < 20 {
		if err := os.WriteFile(behPath, []byte(behavior), 0644); err != nil {
			return "", err
		}
	}
	_ = ensureFile(filepath.Join(dir, FileConstraints), "# Mode constraints\n\n")
	_ = ensureFile(filepath.Join(dir, FileCapabilities), "skills: []\nmcp: []\n")
	// draft marker
	_ = os.WriteFile(filepath.Join(dir, ".draft"), []byte("crystallize_mode\n"), 0644)
	return dir, nil
}
