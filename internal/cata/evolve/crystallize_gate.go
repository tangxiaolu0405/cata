package evolve

import (
	"strings"
)

const (
	crystallizeMinShortBytes = 512
	browserToolRepeatMin     = 3
)

// appendCrystallizeTriggers 根据 short-term 启发式追加固化触发器。
func appendCrystallizeTriggers(s *Snapshot, excerpt string) {
	if s.ShortTermBytes < crystallizeMinShortBytes {
		return
	}
	excerpt = strings.ToLower(excerpt)
	if strings.Count(excerpt, "browser_snapshot") >= browserToolRepeatMin ||
		strings.Count(excerpt, "browser_navigate") >= browserToolRepeatMin {
		s.Triggers = append(s.Triggers, "repeated_browser_tools")
	}
	for _, kw := range []string{"涨停", "连板", "东财", "eastmoney", "zhangting"} {
		if strings.Contains(excerpt, kw) {
			s.Triggers = append(s.Triggers, "task_keyword:"+kw)
			break
		}
	}
}

func shouldInvokeCrystallize(s *Snapshot) bool {
	for _, t := range s.Triggers {
		if strings.HasPrefix(t, "repeated_") || strings.HasPrefix(t, "task_keyword:") ||
			t == "high_token_session" {
			return true
		}
	}
	return false
}
