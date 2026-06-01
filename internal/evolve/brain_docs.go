package evolve

import (
	"path/filepath"

	"cata/internal/brain"
)

func brainDocsNeedingFill() []string {
	w := brain.Active()
	if w == nil {
		return nil
	}
	var out []string
	check := func(label, path string) {
		if brain.FileNeedsEvolveFill(path) {
			out = append(out, label)
		}
	}
	check("persona.local.md", w.PersonaLocalPath())
	modeDir := filepath.Dir(w.PersonaPath())
	check("modes/.../behavior.md", filepath.Join(modeDir, brain.FileBehavior))
	check("modes/.../constraints.md", filepath.Join(modeDir, brain.FileConstraints))
	check("global/constraints.md", brain.GlobalConstraintsPath())
	check("global/behavior.md", brain.GlobalBehaviorPath())
	return out
}
