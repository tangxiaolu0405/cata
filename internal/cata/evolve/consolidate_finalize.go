package evolve

import (
	"strings"

	"cata/internal/cata/brain"
)

// shouldFinalizeShortTerm 演进已成功提炼后，归档并缩短 short-term，避免下轮重复喂 LLM。
func shouldFinalizeShortTerm(dec *Decision, touched []string, snap *Snapshot, sessionCompress bool) bool {
	if snap.ShortTermBytes < int64(ShortTermActivityBytes()) {
		return false
	}
	action := strings.ToLower(strings.TrimSpace(dec.Action))
	if sessionCompress {
		return action == "consolidate" || len(touched) > 0
	}
	if action != "consolidate" {
		return false
	}
	if len(touched) == 0 {
		return false
	}
	for _, p := range touched {
		pl := strings.ToLower(p)
		if strings.Contains(pl, "persona") || strings.HasPrefix(pl, brain.RelMemoryLong+"/") {
			return true
		}
		if pl == brain.RelPersonaLocal {
			return true
		}
	}
	return false
}

func archRel(touched []string) string {
	for _, p := range touched {
		if strings.HasPrefix(p, brain.RelMemoryArchive+"/consolidated-") {
			return p
		}
		// legacy: short-term 曾归档到 memory/long/
		if strings.HasPrefix(p, brain.RelMemoryLong+"/consolidated-") {
			return p
		}
	}
	return ""
}
