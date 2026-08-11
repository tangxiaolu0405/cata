package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"cata/internal/cata/config"
)

func dueSchedule(t *testing.T, id string) *Schedule {
	t.Helper()
	// NextRun 设为过去 → 到点。
	s := &Schedule{
		ID:       id,
		Name:     id,
		Prompt:   "run",
		Cwd:      "/tmp",
		Interval: "1h",
		Enabled:  true,
		NextRun:  time.Now().Add(-time.Minute).Format(time.RFC3339),
	}
	if err := Save(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEngineFiresDueSchedule(t *testing.T) {
	setupSchedulerHome(t)
	s := dueSchedule(t, "fire-me")

	var calls int32
	e := NewEngine(30*time.Second, func(ctx context.Context, got *Schedule) (RunResult, error) {
		atomic.AddInt32(&calls, 1)
		if got.ID != s.ID {
			t.Errorf("run got id %q, want %q", got.ID, s.ID)
		}
		return RunResult{Success: true, Summary: "done"}, nil
	})
	e.tickOnce(context.Background())

	// execute 在 goroutine 里异步完成：轮询等待调用 + last_run 落盘，确保测试结束前无泄漏 goroutine。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		loaded, err := Load(s.ID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.LastRun != nil {
			if atomic.LoadInt32(&calls) != 1 {
				t.Fatalf("calls = %d, want 1", atomic.LoadInt32(&calls))
			}
			if !loaded.LastRun.Success {
				t.Fatalf("last_run success = false, want true")
			}
			if loaded.LastRun.Summary != "done" {
				t.Fatalf("last_run summary = %q, want done", loaded.LastRun.Summary)
			}
			if loaded.NextRun == "" {
				t.Fatal("next_run should be recomputed after run")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run never persisted last_run (calls=%d)", atomic.LoadInt32(&calls))
}

func TestEngineSkipsNotDueAndDisabled(t *testing.T) {
	setupSchedulerHome(t)

	// 未到点：NextRun 在未来。
	future := &Schedule{Name: "future", Prompt: "p", Cwd: "/tmp", Interval: "1h", Enabled: true, NextRun: time.Now().Add(time.Hour).Format(time.RFC3339)}
	if err := Save(future); err != nil {
		t.Fatal(err)
	}
	// 禁用：即使到点也不跑。
	disabled := dueSchedule(t, "disabled")
	disabled.Enabled = false
	if err := Save(disabled); err != nil {
		t.Fatal(err)
	}

	var calls int32
	e := NewEngine(30*time.Second, func(ctx context.Context, got *Schedule) (RunResult, error) {
		atomic.AddInt32(&calls, 1)
		return RunResult{Success: true}, nil
	})
	e.tickOnce(context.Background())
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("calls = %d, want 0 (future/disabled should not fire)", atomic.LoadInt32(&calls))
	}
}

func TestEnginePreventsReentry(t *testing.T) {
	setupSchedulerHome(t)
	dueSchedule(t, "reentry")

	started := make(chan struct{})
	release := make(chan struct{})
	var calls int32
	e := NewEngine(30*time.Second, func(ctx context.Context, got *Schedule) (RunResult, error) {
		atomic.AddInt32(&calls, 1)
		close(started)
		<-release
		return RunResult{Success: true}, nil
	})

	e.tickOnce(context.Background())
	<-started // 第一次运行已进入（running 已置位）
	e.tickOnce(context.Background())
	time.Sleep(50 * time.Millisecond)
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("calls = %d, want 1 (reentry should be prevented)", n)
	}
	close(release)
	// 等待完成并确认清理 running 状态。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !e.isRunning("reentry") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("running flag not cleared after completion")
}

func TestIsDue(t *testing.T) {
	now := time.Now()

	// 过去 next_run → 到点。
	past := &Schedule{NextRun: now.Add(-time.Minute).Format(time.RFC3339)}
	if !isDue(past, now) {
		t.Fatal("past next_run should be due")
	}
	// 未来 next_run → 未到点。
	future := &Schedule{NextRun: now.Add(time.Minute).Format(time.RFC3339)}
	if isDue(future, now) {
		t.Fatal("future next_run should not be due")
	}
	// 恰好到点。
	at := &Schedule{NextRun: now.Format(time.RFC3339)}
	if !isDue(at, now) {
		t.Fatal("next_run == now should be due")
	}
	// 无 next_run + 有效排程 → 补触发（bootstrap）。
	noNext := &Schedule{Interval: "1h"}
	if !isDue(noNext, now) {
		t.Fatal("empty next_run with valid schedule should bootstrap-due")
	}
	// 非法 next_run → 不触发。
	bad := &Schedule{NextRun: "garbage", Interval: "1h"}
	if isDue(bad, now) {
		t.Fatal("malformed next_run should not fire")
	}
}

// TestEngineStartTicks verifies Start runs the loop and stops on ctx cancel.
func TestEngineStartTicks(t *testing.T) {
	setupSchedulerHome(t)
	dueSchedule(t, "start-me")

	ctx, cancel := context.WithCancel(context.Background())
	var calls int32
	e := NewEngine(20*time.Millisecond, func(ctx context.Context, got *Schedule) (RunResult, error) {
		atomic.AddInt32(&calls, 1)
		return RunResult{Success: true}, nil
	})
	e.Start(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&calls) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if atomic.LoadInt32(&calls) == 0 {
		t.Fatal("Start loop never fired a due schedule")
	}
}

var _ = config.EnvCataHome
