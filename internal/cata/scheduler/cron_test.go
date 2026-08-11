package scheduler

import (
	"testing"
	"time"

	"cata/internal/cata/clock"
)

func mustParseCron(t *testing.T, expr string) *CronExpr {
	t.Helper()
	c, err := ParseCron(expr)
	if err != nil {
		t.Fatalf("ParseCron(%q): %v", expr, err)
	}
	return c
}

func TestParseCronValid(t *testing.T) {
	cases := []string{
		"0 9 * * *",
		"*/15 * * * *",
		"0-30 9 * * *",
		"0,30 * * * *",
		"0 9 1 * 1",
		"30 6 * * 7", // 7 = 周日
		"*/5 */2 * * *",
		"0 0 1 1 *",
		"0 9 * * 0", // 0 = 周日
	}
	for _, c := range cases {
		if _, err := ParseCron(c); err != nil {
			t.Errorf("ParseCron(%q) unexpected error: %v", c, err)
		}
	}
}

func TestParseCronInvalid(t *testing.T) {
	cases := []string{
		"",
		"0 9 * *",          // 4 字段
		"0 9 * * * *",      // 6 字段
		"60 9 * * *",       // minute 越界
		"0 24 * * *",       // hour 越界
		"0 9 32 * *",       // day 越界
		"0 9 * 13 *",       // month 越界
		"0 9 * * 8",        // dow 越界
		"0 9 * * x",        // 非数字
		"*/0 * * * *",      // step 0
		"a-b * * * *",      // 非法范围
		"5-1 * * * *",      // 范围倒置
		"0 9 * * ,",        // 空列表项
		"0 9 * * *  extra", // 额外字段会被拆成 6 段
	}
	for _, c := range cases {
		if _, err := ParseCron(c); err == nil {
			t.Errorf("ParseCron(%q) expected error, got nil", c)
		}
	}
}

func TestCronNextDaily(t *testing.T) {
	expr := mustParseCron(t, "0 9 * * *")
	loc := clock.Location()
	after := time.Date(2026, 8, 11, 10, 0, 0, 0, loc)
	next := expr.Next(after)
	want := time.Date(2026, 8, 12, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("Next(%v) = %v, want %v", after, next, want)
	}
}

func TestCronNextStepMinutes(t *testing.T) {
	expr := mustParseCron(t, "*/15 * * * *")
	loc := clock.Location()
	after := time.Date(2026, 8, 11, 10, 7, 0, 0, loc)
	next := expr.Next(after)
	want := time.Date(2026, 8, 11, 10, 15, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("Next(%v) = %v, want %v", after, next, want)
	}
}

func TestCronNextList(t *testing.T) {
	expr := mustParseCron(t, "0,30 * * * *")
	loc := clock.Location()
	after := time.Date(2026, 8, 11, 10, 10, 0, 0, loc)
	next := expr.Next(after)
	want := time.Date(2026, 8, 11, 10, 30, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("Next(%v) = %v, want %v", after, next, want)
	}
	// 10:30 之后 → 下一整点
	after = time.Date(2026, 8, 11, 10, 30, 0, 0, loc)
	next = expr.Next(after)
	want = time.Date(2026, 8, 11, 11, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("Next(%v) = %v, want %v", after, next, want)
	}
}

func TestCronNextRange(t *testing.T) {
	// 0-30 覆盖每分钟：09:20 之后 → 09:21。
	expr := mustParseCron(t, "0-30 9 * * *")
	loc := clock.Location()
	after := time.Date(2026, 8, 11, 9, 20, 0, 0, loc)
	next := expr.Next(after)
	want := time.Date(2026, 8, 11, 9, 21, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("Next(%v) = %v, want %v", after, next, want)
	}
}

func TestCronNextRangeWithStep(t *testing.T) {
	// 0-30/10 = {0,10,20,30}：09:20 之后 → 09:30。
	expr := mustParseCron(t, "0-30/10 9 * * *")
	loc := clock.Location()
	after := time.Date(2026, 8, 11, 9, 20, 0, 0, loc)
	next := expr.Next(after)
	want := time.Date(2026, 8, 11, 9, 30, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("Next(%v) = %v, want %v", after, next, want)
	}
}

func TestCronNextDOMOrDOW(t *testing.T) {
	// 日与周 OR 语义：每月 1 号 或 周一 的 09:00。
	expr := mustParseCron(t, "0 9 1 * 1")
	loc := clock.Location()

	// 2026-08-01 是周六，且是 1 号 → 当天 09:00。
	after := time.Date(2026, 8, 1, 0, 0, 0, 0, loc)
	next := expr.Next(after)
	want := time.Date(2026, 8, 1, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("Next(%v) = %v, want %v", after, next, want)
	}

	// 1 号已过 → 下一个周一（2026-08-03）。
	after = time.Date(2026, 8, 1, 12, 0, 0, 0, loc)
	next = expr.Next(after)
	want = time.Date(2026, 8, 3, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("Next(%v) = %v, want %v", after, next, want)
	}

	// 周一（08-03）已过 → 下一个周一（08-10）早于下月 1 号，OR 语义取更早者。
	after = time.Date(2026, 8, 3, 10, 0, 0, 0, loc)
	next = expr.Next(after)
	want = time.Date(2026, 8, 10, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("Next(%v) = %v, want %v", after, next, want)
	}
}

func TestCronNextSunday7Normalized(t *testing.T) {
	expr := mustParseCron(t, "0 9 * * 7")
	loc := clock.Location()
	// 2026-08-02 是周日。
	after := time.Date(2026, 8, 1, 0, 0, 0, 0, loc)
	next := expr.Next(after)
	want := time.Date(2026, 8, 2, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("Next(%v) = %v, want %v", after, next, want)
	}
}

func TestCronNextWildcard(t *testing.T) {
	expr := mustParseCron(t, "* * * * *")
	loc := clock.Location()
	after := time.Date(2026, 8, 11, 10, 7, 30, 0, loc)
	next := expr.Next(after)
	want := time.Date(2026, 8, 11, 10, 8, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("Next(%v) = %v, want %v", after, next, want)
	}
}
