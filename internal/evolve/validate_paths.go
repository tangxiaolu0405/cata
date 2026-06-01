package evolve

import (
	"strings"

	"cata/internal/brain"
)

// evolutionRequiresSectionedUpdate 必须用 append_section/replace_section，禁止尾部 append。
func evolutionRequiresSectionedUpdate(rel string) bool {
	switch rel {
	case brain.RelPersonaLocal, "global/constraints.md", "global/behavior.md":
		return true
	}
	if strings.Contains(rel, "/"+brain.FilePersona) {
		return true
	}
	prefix := brain.DirModes + "/"
	if strings.HasPrefix(rel, prefix) && strings.HasSuffix(rel, "/"+brain.FileBehavior) {
		return true
	}
	if strings.HasPrefix(rel, prefix) && strings.HasSuffix(rel, "/"+brain.FileConstraints) {
		return true
	}
	return false
}
