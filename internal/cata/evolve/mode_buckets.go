package evolve

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"cata/internal/cata/brain"
)

var (
	reDelegateModeNote = regexp.MustCompile(`\[delegate_mode mode=([^\s]+) case=([^\s]+) id=([^\s]+) status=([^\s\]]+)\]`)
	// short-term / archive 回合标题：## 2026-07-24T19:15:23+08:00
	reSessionDayHeader = regexp.MustCompile(`(?m)^## (\d{4}-\d{2}-\d{2})`)
	// FinalizeShortTerm 归档名：consolidated-2026-07-24-190535.md
	reConsolidatedDay = regexp.MustCompile(`^consolidated-(\d{4}-\d{2}-\d{2})`)
)

// 跨日重复干活达到此天数 → 可结晶专职（适配「每日一次」而非「同会话连聊」）。
const crystallizeMinDistinctDays = 3

// ModeBucketStats 某 mode 的可观察委托统计（供分桶 evolve）。
type ModeBucketStats struct {
	ModeID   string `json:"mode_id"`
	Runs     int    `json:"runs"`
	Failures int    `json:"failures"`
	Excerpt  string `json:"excerpt,omitempty"`
}

func observeModeBuckets(s *Snapshot, ws *brain.Workspace) {
	if s == nil || ws == nil {
		return
	}
	buckets := map[string]*ModeBucketStats{}

	var shortBody string
	if data, err := os.ReadFile(ws.ShortTermPath()); err == nil {
		shortBody = string(data)
		for _, m := range reDelegateModeNote.FindAllStringSubmatch(shortBody, -1) {
			if len(m) < 5 {
				continue
			}
			id := brain.ResolveDelegateModeID(m[1])
			b := buckets[id]
			if b == nil {
				b = &ModeBucketStats{ModeID: id}
				buckets[id] = b
			}
			b.Runs++
			if m[4] != "ok" {
				b.Failures++
			}
			line := strings.TrimSpace(m[0])
			if len(line) > 160 {
				line = line[:160] + "…"
			}
			if b.Excerpt == "" {
				b.Excerpt = line
			}
		}
	}

	casesRoot := filepath.Join(ws.RootPath, brain.DirCases)
	_ = filepath.Walk(casesRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(path), "/")
		for i := 0; i+2 < len(parts); i++ {
			if parts[i] != "mode_runs" {
				continue
			}
			mode := brain.ResolveDelegateModeID(parts[i+1])
			b := buckets[mode]
			if b == nil {
				b = &ModeBucketStats{ModeID: mode}
				buckets[mode] = b
			}
			b.Runs++
			if data, err := os.ReadFile(path); err == nil {
				low := strings.ToLower(string(data))
				if strings.Contains(low, "status: failed") || strings.Contains(low, "status: cancelled") {
					b.Failures++
				}
			}
			break
		}
		return nil
	})

	orchHeavy := 0
	specialistRuns := 0
	if len(buckets) > 0 {
		s.ModeBuckets = make([]ModeBucketStats, 0, len(buckets))
		for _, b := range buckets {
			s.ModeBuckets = append(s.ModeBuckets, *b)
			if b.ModeID == brain.ModeDefaultID {
				orchHeavy += b.Runs
			} else {
				specialistRuns += b.Runs
			}
		}
	}

	maybeAppendCrystallizeModeCandidate(s, ws, shortBody, specialistRuns)

	for _, b := range s.ModeBuckets {
		if b.Runs >= 3 {
			s.Triggers = append(s.Triggers, "mode_bucket:"+b.ModeID)
		}
		if b.Runs >= 2 && b.Failures*2 >= b.Runs {
			s.Triggers = append(s.Triggers, "mode_reject_high:"+b.ModeID)
		}
	}
	if orchHeavy >= 2 {
		s.Triggers = append(s.Triggers, "orch_bucket")
	}
}

func maybeAppendCrystallizeModeCandidate(s *Snapshot, ws *brain.Workspace, shortBody string, specialistRuns int) {
	if specialistRuns > 0 {
		return
	}
	if shortBody == "" {
		if data, err := os.ReadFile(ws.ShortTermPath()); err == nil {
			shortBody = string(data)
		}
	}
	low := strings.ToLower(shortBody)
	if strings.Contains(low, "delegate_mode") {
		return
	}

	days := collectRecurringJobDays(ws, shortBody)
	denseSession := s.ShortTermBytes >= crystallizeMinShortBytes &&
		strings.Count(low, "**assistant:**") >= 4
	crossDay := days >= crystallizeMinDistinctDays

	if !denseSession && !crossDay {
		return
	}
	if crossDay {
		s.Triggers = append(s.Triggers, "crystallize_mode_candidate")
		s.Triggers = append(s.Triggers, "recurring_job_days")
		return
	}
	s.Triggers = append(s.Triggers, "crystallize_mode_candidate")
}

// collectRecurringJobDays 统计「干过活」的不重复日历日：当前 short-term + 近期 short 归档 + session-notes。
// 适配每日一次同类任务；不要求同一会话连聊多轮。
func collectRecurringJobDays(ws *brain.Workspace, shortBody string) int {
	days := map[string]struct{}{}
	addDaysFromText(days, shortBody)

	archDir := ws.ArchiveDir()
	ents, err := os.ReadDir(archDir)
	if err == nil {
		// 新文件在后；最多扫 40 个 consolidated
		n := 0
		for i := len(ents) - 1; i >= 0 && n < 40; i-- {
			name := ents[i].Name()
			if ents[i].IsDir() || !strings.HasSuffix(name, ".md") {
				continue
			}
			if m := reConsolidatedDay.FindStringSubmatch(name); len(m) == 2 {
				days[m[1]] = struct{}{}
				n++
				continue
			}
			if !strings.HasPrefix(name, "consolidated-") {
				continue
			}
			n++
			if data, err := os.ReadFile(filepath.Join(archDir, name)); err == nil {
				addDaysFromText(days, string(data))
			}
		}
	}

	if data, err := os.ReadFile(ws.Path(brain.RelMemoryLongSessionNotes)); err == nil {
		addDaysFromText(days, string(data))
	}
	return len(days)
}

func addDaysFromText(days map[string]struct{}, text string) {
	for _, m := range reSessionDayHeader.FindAllStringSubmatch(text, -1) {
		if len(m) == 2 {
			days[m[1]] = struct{}{}
		}
	}
}
