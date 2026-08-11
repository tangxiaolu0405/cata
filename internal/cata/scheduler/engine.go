package scheduler

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"cata/internal/cata/clock"
)

// RunResult 一次定时执行的结果（由执行回调返回，落盘到排程 last_run）。
type RunResult struct {
	Success    bool
	Summary    string
	ReportPath string
}

// RunFunc 到点执行回调（由 server 提供：离线跑完整 chat 循环）。
type RunFunc func(ctx context.Context, s *Schedule) (RunResult, error)

// Engine 定时触发引擎：按 tick 间隔扫描 schedules 目录，
// 到点（NextFire <= now）且未在运行（防重入）的任务触发执行。
type Engine struct {
	tick    time.Duration
	run     RunFunc
	mu      sync.Mutex
	running map[string]bool
}

// NewEngine 新建引擎；tick<=0 时默认 30s。
func NewEngine(tick time.Duration, run RunFunc) *Engine {
	if tick <= 0 {
		tick = 30 * time.Second
	}
	return &Engine{tick: tick, run: run, running: map[string]bool{}}
}

// Start 在后台 goroutine 中启动 tick 循环。
func (e *Engine) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(e.tick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.tickOnce(ctx)
			}
		}
	}()
}

// runKey 唯一标识一条排程（同 id 可存在于不同项目，用 Project 区分）。
func runKey(s *Schedule) string {
	return s.ID + "\x00" + s.Project
}

func (e *Engine) setRunning(id string, v bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if v {
		e.running[id] = true
	} else {
		delete(e.running, id)
	}
}

func (e *Engine) isRunning(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running[id]
}

func (e *Engine) tickOnce(ctx context.Context) {
	all, err := ListAll()
	if err != nil {
		log.Printf("scheduler: list schedules: %v", err)
		return
	}
	now := time.Now()
	for _, s := range all {
		if !s.Enabled {
			continue
		}
		key := runKey(s)
		if e.isRunning(key) {
			continue
		}
		if !isDue(s, now) {
			continue
		}
		e.setRunning(key, true)
		go func(s *Schedule) {
			defer e.setRunning(key, false)
			e.execute(ctx, s)
		}(s)
	}
}

// isDue 判断某条排程是否到点。优先用落盘的 next_run（Save 时已按当前时间算好）；
// 无 next_run（手工编写/旧版文件）时按有效排程补触发一次，由 execute 回写基线。
func isDue(s *Schedule, now time.Time) bool {
	if tr := strings.TrimSpace(s.NextRun); tr != "" {
		t, err := time.Parse(time.RFC3339, tr)
		if err != nil {
			return false
		}
		return !now.Before(t)
	}
	if _, err := s.NextFire(now); err == nil {
		return true
	}
	return false
}

// RunOnce 扫描环境中到点的排程并同步执行一轮（cata schedule --once，供系统 cron 等外部调度触发）。
// 返回本次实际执行的任务数；防重入仅对本进程内生效。
func (e *Engine) RunOnce(ctx context.Context) (int, error) {
	all, err := ListAll()
	if err != nil {
		return 0, err
	}
	now := time.Now()
	count := 0
	for _, s := range all {
		if !s.Enabled {
			continue
		}
		key := runKey(s)
		if e.isRunning(key) {
			continue
		}
		if !isDue(s, now) {
			continue
		}
		e.setRunning(key, true)
		e.execute(ctx, s)
		e.setRunning(key, false)
		count++
	}
	return count, nil
}

// execute 执行一次任务并回写 last_run / next_run。
func (e *Engine) execute(ctx context.Context, s *Schedule) {
	if e.run == nil {
		log.Printf("scheduler: no run callback for %s", s.ID)
		return
	}
	started := clock.RFC3339()
	res, err := e.run(ctx, s)
	info := &RunInfo{At: started, Success: err == nil && res.Success}
	if res.Summary != "" {
		info.Summary = truncateRunes(res.Summary, 400)
	}
	if res.ReportPath != "" {
		info.Report = res.ReportPath
	}
	loaded, lerr := Load(s.ID)
	if lerr != nil || loaded == nil {
		loaded = s
	}
	loaded.LastRun = info
	if next, nerr := loaded.NextFire(clock.Now()); nerr == nil {
		loaded.NextRun = clock.FormatTime(next, time.RFC3339)
	}
	if serr := Save(loaded); serr != nil {
		log.Printf("scheduler: persist run result for %s: %v", s.ID, serr)
	}
	if err != nil {
		log.Printf("scheduler: run %s failed: %v", s.ID, err)
	} else if !res.Success {
		log.Printf("scheduler: run %s completed unsuccessfully: %s", s.ID, res.Summary)
	}
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
