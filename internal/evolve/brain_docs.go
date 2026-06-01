package evolve

import (
	"path/filepath"

	"cata/internal/brain"
)

type brainDocFillCheck struct {
	label   string
	trigger string
	path    string
}

// brainDocFillChecks 仅 workspace 格子内文档；global/* 为全机共享，不由 per-workspace evolve 填充。
func brainDocFillChecks(w *brain.Workspace) []brainDocFillCheck {
	modeDir := filepath.Dir(w.PersonaPath())
	return []brainDocFillCheck{
		{"persona.local.md", "fill:persona.local", w.PersonaLocalPath()},
		{"modes/.../behavior.md", "fill:mode_behavior", filepath.Join(modeDir, brain.FileBehavior)},
		{"modes/.../constraints.md", "fill:mode_constraints", filepath.Join(modeDir, brain.FileConstraints)},
	}
}

func appendBrainDocFillTriggers(s *Snapshot, ws *brain.Workspace) {
	if s.ShortTermBytes < shortTermActivityBytes || ws == nil {
		return
	}
	for _, c := range brainDocFillChecks(ws) {
		if brain.FileNeedsEvolveFill(c.path) {
			s.Triggers = append(s.Triggers, c.trigger)
		}
	}
}

func brainDocsNeedingFill(ws *brain.Workspace) []string {
	if ws == nil {
		return nil
	}
	var out []string
	for _, c := range brainDocFillChecks(ws) {
		if brain.FileNeedsEvolveFill(c.path) {
			out = append(out, c.label)
		}
	}
	return out
}
