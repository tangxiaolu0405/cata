package evolve

import (
	"fmt"
	"strings"
	"time"

	"cata/internal/brain"
)

// shouldInvokeLLM 在调用模型前做确定性门控，避免空转与重复扫描。
// hot.md 由本模块写入，不作为「hot 已改」触发条件。
func shouldInvokeLLM(snap *Snapshot, cooldownUntil time.Time, lastFingerprint string) (bool, string) {
	if time.Now().Before(cooldownUntil) {
		return false, "cooldown after last patch"
	}
	if hasFillTrigger(snap) {
		return true, strings.Join(snap.Triggers, ", ")
	}
	fp := snap.Fingerprint()
	if fp != "" && fp == lastFingerprint {
		return false, "inputs unchanged since last cycle (short/long)"
	}
	if len(snap.Triggers) == 0 {
		return false, "no triggers (need short-term activity or large long-term)"
	}
	return true, strings.Join(snap.Triggers, ", ")
}

func hasFillTrigger(snap *Snapshot) bool {
	for _, t := range snap.Triggers {
		if strings.HasPrefix(t, "fill:") {
			return true
		}
	}
	return false
}

// computeTriggers 根据「待提炼进 hot 的输入」判断，不观测 hot 是否被改过。
func computeTriggers(s *Snapshot, ws *brain.Workspace) {
	s.Triggers = nil

	if s.ShortTermBytes >= shortTermTriggerBytes {
		s.Triggers = append(s.Triggers, fmt.Sprintf("short_term>=%dB", shortTermTriggerBytes))
	} else if s.ShortTermBytes >= shortTermActivityBytes {
		if s.LastEvolutionAt == "" {
			s.Triggers = append(s.Triggers, fmt.Sprintf("short_term>=%dB_first", shortTermActivityBytes))
		} else if s.ShortTermModTime != "" && s.ShortTermModTime > s.LastEvolutionAt {
			s.Triggers = append(s.Triggers, "short_term_updated_since_evolution")
		}
	}

	if s.LongTermFileCount >= longTermSummarizeMinFiles {
		s.Triggers = append(s.Triggers, fmt.Sprintf("long_term>=%d", longTermSummarizeMinFiles))
	}

	appendBrainDocFillTriggers(s, ws)
}
