package evolve

import (
	"path/filepath"
	"strings"
	"unicode/utf8"

	"cata/internal/brain"
)

func filterUpdates(updates []DocUpdate) []DocUpdate {
	return filterUpdatesWithLimit(updates, maxUpdatesPerCycle)
}

func filterUpdatesCrystallize(updates []DocUpdate) []DocUpdate {
	return filterUpdatesWithLimit(updates, maxCrystallizeUpdatesPerCycle)
}

func filterUpdatesWithLimit(updates []DocUpdate, limit int) []DocUpdate {
	var out []DocUpdate
	for _, u := range updates {
		content := strings.TrimSpace(u.Content)
		if u.Mode != "write" && u.Mode != "overwrite" && utf8.RuneCountInString(content) < minPatchContentRunes {
			continue
		}
		rel := strings.TrimPrefix(strings.TrimSpace(u.Path), "brain/")
		rel = filepath.ToSlash(filepath.Clean(rel))
		if err := brain.RejectCapabilitiesPatch(rel, u.Mode, content); err != nil {
			continue
		}
		mode := strings.ToLower(strings.TrimSpace(u.Mode))
		if evolutionRequiresSectionedUpdate(rel) {
			if mode == "append" || mode == "" {
				continue
			}
		}
		if strings.Contains(rel, "/"+brain.FilePersona) {
			if (mode == "write" || mode == "overwrite") && utf8.RuneCountInString(content) > 6500 {
				continue
			}
		}
		if rel == brain.RelPersonaLocal {
			if (mode == "write" || mode == "overwrite") && utf8.RuneCountInString(content) > 2000 {
				continue
			}
		}
		if strings.HasSuffix(rel, "/"+brain.FileBehavior) {
			if (mode == "write" || mode == "overwrite") && utf8.RuneCountInString(content) > 2000 {
				continue
			}
		}
		if strings.HasSuffix(rel, "/"+brain.FileConstraints) {
			if mode == "write" || mode == "overwrite" {
				continue
			}
			if utf8.RuneCountInString(content) > 800 {
				continue
			}
		}
		if rel == "global/constraints.md" || rel == "global/behavior.md" {
			if mode == "write" || mode == "overwrite" {
				continue
			}
			if utf8.RuneCountInString(content) > 600 {
				continue
			}
		}
		if rel == brain.RelMetaJSON &&
			(strings.EqualFold(u.Mode, "write") || strings.EqualFold(u.Mode, "overwrite")) &&
			utf8.RuneCountInString(content) > 2000 {
			continue
		}
		out = append(out, u)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func isMeaningfulDecision(dec *Decision, touched []string) bool {
	action := strings.ToLower(strings.TrimSpace(dec.Action))
	if action == "" || action == "idle" {
		return len(touched) > 0
	}
	return len(touched) > 0 || utf8.RuneCountInString(strings.TrimSpace(dec.Learning)) >= minPatchContentRunes
}
