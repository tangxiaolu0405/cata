package gateway

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"cata/internal/cata/brain"
)

// maxWorkdirCandidates /dir 列表最多列出的已注册工作区数。
const maxWorkdirCandidates = 10

// workspaceCandidates 本机已注册工作区（按最近使用倒序，最多 maxWorkdirCandidates）。
func workspaceCandidates() []brain.RegistryEntry {
	entries, err := brain.ListRegistryEntries()
	if err != nil {
		return nil
	}
	var out []brain.RegistryEntry
	for _, e := range entries {
		if strings.TrimSpace(e.RootPath) == "" {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return lastSeenTime(out[i].LastSeenAt).After(lastSeenTime(out[j].LastSeenAt))
	})
	if len(out) > maxWorkdirCandidates {
		out = out[:maxWorkdirCandidates]
	}
	return out
}

func lastSeenTime(rfc string) time.Time {
	ts, err := time.Parse(time.RFC3339, rfc)
	if err != nil {
		return time.Time{}
	}
	return ts
}

// relSeen LastSeenAt 相对时间（x 分钟 / x 小时 / 日期）。
func relSeen(rfc string) string {
	ts := lastSeenTime(rfc)
	if ts.IsZero() {
		return "未知"
	}
	d := time.Since(ts)
	switch {
	case d < 0:
		return "刚刚"
	case d < 1*time.Minute:
		return "刚刚"
	case d < 1*time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	}
}
