package scheduler

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"cata/internal/cata/clock"
)

// CronExpr 5 字段 cron（分 时 日 月 周）。支持 *、*/n、n、a-b、a-b/n、a,b。
// 日与周同时受限时按标准 cron 的 OR 语义匹配（任一命中即可）。
type CronExpr struct {
	minutes     map[int]bool
	hours       map[int]bool
	days        map[int]bool
	months      map[int]bool
	dows        map[int]bool
	domWildcard bool
	dowWildcard bool
}

type cronFieldSpec struct {
	name  string
	min   int
	max   int
	width int // 周期（分钟/小时/日/月/周）
}

var cronFields = []cronFieldSpec{
	{name: "minute", min: 0, max: 59, width: 60},
	{name: "hour", min: 0, max: 23, width: 24},
	{name: "day-of-month", min: 1, max: 31, width: 31},
	{name: "month", min: 1, max: 12, width: 12},
	{name: "day-of-week", min: 0, max: 7, width: 8}, // 7 归一化为 0（周日）
}

// ParseCron 解析 5 字段 cron 表达式（空格分隔）。
func ParseCron(expr string) (*CronExpr, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return nil, fmt.Errorf("expected 5 fields, got %d", len(fields))
	}
	c := &CronExpr{
		minutes: map[int]bool{},
		hours:   map[int]bool{},
		days:    map[int]bool{},
		months:  map[int]bool{},
		dows:    map[int]bool{},
	}
	sets := []*map[int]bool{&c.minutes, &c.hours, &c.days, &c.months, &c.dows}
	for i, raw := range fields {
		set, wildcard, err := parseCronField(raw, cronFields[i])
		if err != nil {
			return nil, fmt.Errorf("field %d (%s): %w", i+1, cronFields[i].name, err)
		}
		*sets[i] = set
		switch i {
		case 2:
			c.domWildcard = wildcard
		case 4:
			c.dowWildcard = wildcard
		}
	}
	// 日/周全匹配时无需具体集合，但保留集合便于统一逻辑。
	return c, nil
}

func parseCronField(raw string, spec cronFieldSpec) (map[int]bool, bool, error) {
	set := map[int]bool{}
	wildcard := false
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false, fmt.Errorf("empty list item in %q", raw)
		}
		step := 1
		base := part
		if i := strings.Index(part, "/"); i >= 0 {
			base = part[:i]
			s, err := strconv.Atoi(part[i+1:])
			if err != nil || s <= 0 {
				return nil, false, fmt.Errorf("bad step in %q", part)
			}
			step = s
		}
		var lo, hi int
		switch {
		case base == "*":
			lo, hi = spec.min, spec.max
			if step == 1 {
				wildcard = true
			}
		case strings.Contains(base, "-"):
			bounds := strings.SplitN(base, "-", 2)
			var err error
			lo, err = strconv.Atoi(bounds[0])
			if err != nil {
				return nil, false, fmt.Errorf("bad range in %q", part)
			}
			hi, err = strconv.Atoi(bounds[1])
			if err != nil {
				return nil, false, fmt.Errorf("bad range in %q", part)
			}
		default:
			v, err := strconv.Atoi(base)
			if err != nil {
				return nil, false, fmt.Errorf("bad value in %q", part)
			}
			lo, hi = v, v
		}
		if spec.name == "day-of-week" {
			// 允许 7 = 周日。
			if lo == 7 {
				lo = 0
			}
			if hi == 7 {
				hi = 0
			}
		}
		if lo < spec.min || hi > spec.max || lo > hi {
			return nil, false, fmt.Errorf("value %d-%d out of range [%d,%d]", lo, hi, spec.min, spec.max)
		}
		for v := lo; v <= hi; v += step {
			if spec.name == "day-of-week" && v > spec.max {
				continue
			}
			set[v] = true
		}
	}
	return set, wildcard, nil
}

// Next 返回 after 之后（严格大于）的下一触发时刻，按配置时区计算。
// 找不到（表达式永不可能触发）返回零值 time.Time。
func (c *CronExpr) Next(after time.Time) time.Time {
	loc := clock.Location()
	start := after.In(loc).Add(time.Minute)
	t := time.Date(start.Year(), start.Month(), start.Day(), start.Hour(), start.Minute(), 0, 0, loc)
	// 最多扫 5 年（覆盖闰年）；合法表达式必然在此范围内命中。
	for day := 0; day < 5*366; day++ {
		if !c.months[int(t.Month())] || !c.dayMatches(t) {
			t = t.AddDate(0, 0, 1)
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
			continue
		}
		if !c.hours[t.Hour()] {
			t = t.Add(time.Hour)
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc)
			continue
		}
		if !c.minutes[t.Minute()] {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

func (c *CronExpr) dayMatches(t time.Time) bool {
	domOK := c.days[t.Day()]
	dowOK := c.dows[int(t.Weekday())]
	if !c.domWildcard && !c.dowWildcard {
		return domOK || dowOK
	}
	if !c.domWildcard {
		return domOK
	}
	if !c.dowWildcard {
		return dowOK
	}
	return true
}
