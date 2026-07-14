package evolve

import (
	"path/filepath"

	"cata/internal/cata/brain"
)

type brainDocFillCheck struct {
	label   string
	trigger string
	path    string
}

// brainDocFillChecks 项目 .cata 内 active mode 主要内容；global/* 引导不由 per-workspace evolve 填充。
func brainDocFillChecks(w *brain.Workspace) []brainDocFillCheck {
	mode := brain.NormalizeModeID(w.ActiveMode)
	modeDir := w.ModeDir(mode)
	modeRel := "modes/" + mode + "/"
	return []brainDocFillCheck{
		{modeRel + "persona.md", "fill:mode_persona", filepath.Join(modeDir, brain.FilePersona)},
		{"persona.local.md", "fill:persona.local", w.PersonaLocalPath()},
		{modeRel + "behavior.md", "fill:mode_behavior", filepath.Join(modeDir, brain.FileBehavior)},
		{modeRel + "constraints.md", "fill:mode_constraints", filepath.Join(modeDir, brain.FileConstraints)},
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
