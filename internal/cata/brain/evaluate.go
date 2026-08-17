package brain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cata/internal/cata/clock"
)

// P4⑦ Evaluate：把「命中观测」升级为「记→用→验」闭环——
// 检索命中记录到 per-ws 命中日志，evolve 周期统计：周期内频繁命中的记忆强化
// （priority+1），长期未命中且陈旧的僵尸记忆降权（priority-1）。

const (
	hitsLogName      = "memory/hits.jsonl"
	evaluateHitBoost = 3  // 单周期内命中 ≥ 该次数 → 强化
	maxPriorityCap   = 9  // priority 上限（persona=9 最高）
	zombieStaleDays  = 60 // 超过该天数未命中且无历史命中 → 僵尸降权
)

// RecordRetrievalHits 记录一次检索命中的记忆来源（命中观测，供 Evaluate）。
// 幂等：sources 为空或 ws 为 nil 时 no-op。
func RecordRetrievalHits(w *Workspace, sources []string) error {
	if w == nil || len(sources) == 0 {
		return nil
	}
	abs := w.Path(hitsLogName)
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	ts := clock.RFC3339()
	enc := json.NewEncoder(f)
	for _, s := range sources {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		_ = enc.Encode(map[string]string{"source": s, "t": ts})
	}
	return nil
}

// EvaluateIndex 根据命中日志评估记忆条目：频繁命中的强化、陈旧未命中的僵尸降权，
// 并把累计命中写回 entry.Hits。评估后清空命中日志（每周期一轮）。
func EvaluateIndex(w *Workspace) error {
	if w == nil {
		return nil
	}
	counts, err := readHitsCounts(w.Path(hitsLogName))
	if err != nil {
		return err
	}
	if len(counts) == 0 {
		return nil // 无命中记录，不空转读写 index
	}
	idx, err := LoadMemoryIndexFor(w)
	if err != nil {
		return err
	}
	now := time.Now()
	changed := false
	for i := range idx.Entries {
		e := &idx.Entries[i]
		rel := filepath.ToSlash(strings.TrimSpace(e.Source))
		if n, ok := counts[rel]; ok && n > 0 {
			e.Hits += n
			if e.Hits >= evaluateHitBoost && e.Priority < maxPriorityCap {
				e.Priority++ // 周期内频繁命中 → 强化
				changed = true
			}
			continue
		}
		// 僵尸降权：从未命中且条目已陈旧 → 降权（多次 Evaluate 后可沉底）。
		if e.Hits == 0 && isStaleEntry(e.UpdatedAt, now) && e.Priority > 0 {
			e.Priority--
			changed = true
		}
	}
	if changed {
		if err := SaveMemoryIndexFor(w, idx); err != nil {
			return err
		}
	}
	_ = os.Remove(w.Path(hitsLogName))
	return nil
}

func readHitsCounts(path string) (map[string]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	counts := map[string]int{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec struct {
			Source string `json:"source"`
		}
		if json.Unmarshal([]byte(line), &rec) == nil && rec.Source != "" {
			counts[rec.Source]++
		}
	}
	return counts, nil
}

func isStaleEntry(updatedAt string, now time.Time) bool {
	if updatedAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		return false
	}
	return now.Sub(t) > zombieStaleDays*24*time.Hour
}
