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

// filterUpdatesForDecision 按 action / target_mode / new_mode_id 收窄可写路径。
func filterUpdatesForDecision(dec *Decision, updates []DocUpdate) []DocUpdate {
	base := filterUpdates(updates)
	if dec == nil {
		return base
	}
	action := strings.ToLower(strings.TrimSpace(dec.Action))
	switch action {
	case "crystallize_mode":
		return filterUpdatesCrystallizeMode(dec, base)
	case "crystallize", "crystallize_skill":
		if id := resolveCrystallizeModeID(dec); id != "" {
			return filterPathPrefix(base, "modes/"+id+"/")
		}
		return filterPathPrefix(base, "skills/")
	case "mode_evolve", "evolve_mode":
		id := brain.ResolveDelegateModeID(dec.TargetMode)
		if id == "" {
			return base
		}
		return filterPathPrefix(base, "modes/"+id+"/")
	case "orch_evolve", "evolve_orch":
		return filterPathPrefix(base, "modes/"+brain.ModeDefaultID+"/")
	case "consolidate":
		return filterUpdatesConsolidate(dec, base)
	default:
		return filterUpdatesConsolidate(dec, base)
	}
}

func filterUpdatesConsolidate(dec *Decision, base []DocUpdate) []DocUpdate {
	if tm := brain.ResolveDelegateModeID(dec.TargetMode); tm != "" && tm != brain.ModeDefaultID {
		return filterPathPrefix(base, "modes/"+tm+"/")
	}
	return base
}

func filterUpdatesCrystallizeMode(dec *Decision, base []DocUpdate) []DocUpdate {
	id := resolveCrystallizeModeID(dec)
	if id == "" {
		return nil
	}
	return filterPathPrefix(base, "modes/"+id+"/")
}

func resolveCrystallizeModeID(dec *Decision) string {
	if dec == nil {
		return ""
	}
	id := strings.TrimSpace(dec.NewModeID)
	if id == "" {
		id = strings.TrimSpace(dec.TargetMode)
	}
	id = brain.ResolveDelegateModeID(id)
	if id == "" || id == brain.ModeDefaultID {
		return ""
	}
	return id
}

func filterPathPrefix(updates []DocUpdate, prefix string) []DocUpdate {
	var out []DocUpdate
	for _, u := range updates {
		rel, err := brain.NormalizeEvolveUpdatePath(u.Path)
		if err != nil {
			continue
		}
		if strings.HasPrefix(rel, prefix) {
			out = append(out, u)
		}
	}
	return out
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
