package evolve

import (
	"strings"
	"unicode/utf8"

	"cata/internal/cata/brain"
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
		mode := strings.ToLower(strings.TrimSpace(u.Mode))
		content := strings.TrimSpace(u.Content)
		if mode != "write" && mode != "overwrite" && mode != "delete" && mode != "delete_section" &&
			utf8.RuneCountInString(content) < minPatchContentRunes {
			continue
		}
		rel, err := brain.NormalizeEvolveUpdatePath(u.Path)
		if err != nil {
			continue
		}
		if err := brain.RejectEvolveSharedGlobalPatch(rel); err != nil {
			continue
		}
		if err := brain.RejectCapabilitiesPatch(rel, u.Mode, content); err != nil {
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
