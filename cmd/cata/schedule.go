package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"cata/internal/cata/brain"
	"cata/internal/cata/client"
	"cata/internal/cata/config"
	"cata/internal/cata/scheduler"
	"cata/internal/cata/scheduler/runner"
	"cata/internal/cata/server"
)

// runSchedule 启动调度框架守护进程：发现环境（机器 + 项目 .cata/schedules）中的任务，
// 到点后作为真实 socket 客户端自发起一轮 chat（见 internal/cata/scheduler/runner）。
//   - --dir <path>  额外产出区/项目根（会注册进工作区 registry，供项目级排程发现）
//   - --once        扫描到点任务并同步执行一轮后退出（可挂系统 cron）
//   - --tick <sec>  引擎扫描周期（默认 config.schedules.tick_seconds，缺省 30s）
func runSchedule(args []string) {
	var once bool
	var tickSec int
	var dirs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--once":
			once = true
		case a == "--tick" && i+1 < len(args):
			if v, err := strconv.Atoi(args[i+1]); err == nil && v > 0 {
				tickSec = v
				i++
			}
		case a == "--dir" && i+1 < len(args):
			dirs = append(dirs, args[i+1])
			i++
		case a == "-h" || a == "--help":
			fmt.Println("Usage: cata schedule [--once] [--tick <sec>] [--dir <path>]")
			fmt.Println("  Self-hosted scheduler: discovers scheduled tasks (machine + project .cata/schedules)")
			fmt.Println("  and fires them at trigger time as a real cata client.")
			return
		default:
			fmt.Fprintf(os.Stderr, "cata schedule: unknown flag %q\n", a)
			os.Exit(2)
		}
	}

	// 注册额外产出区/项目根，使项目级排程可被发现。
	for _, d := range dirs {
		if _, err := brain.ResolveWorkspace(d); err != nil {
			log.Printf("cata schedule: resolve --dir %s: %v", d, err)
		}
	}

	if !config.SchedulesEnabled() {
		log.Println("cata schedule: schedules disabled in config (schedules.enabled=false)")
		return
	}

	tick := config.SchedulesTick()
	if tickSec > 0 {
		tick = time.Duration(tickSec) * time.Second
	}

	// 自托管：如无 server 则本进程内置一个（cata run 语义）；已有则复用。
	srv, err := ensureServer()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cata schedule: ensure server: %v\n", err)
		os.Exit(1)
	}
	socketPath := config.ResolvedSocketPath()

	engine := scheduler.NewEngine(tick, func(ctx context.Context, s *scheduler.Schedule) (scheduler.RunResult, error) {
		if err := ensureServerRunning(); err != nil {
			return scheduler.RunResult{}, err
		}
		return runner.Run(ctx, s, socketPath)
	})

	if once {
		ctx, cancel := context.WithCancel(context.Background())
		n, err := engine.RunOnce(ctx)
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "cata schedule --once: %v\n", err)
		}
		log.Printf("cata schedule --once: executed %d due task(s)", n)
		stopServer(srv)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	engine.Start(ctx)
	log.Printf("cata schedule: scheduler daemon started (tick=%s, socket=%s)", tick, socketPath)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	cancel()
	stopServer(srv)
	log.Println("cata schedule: stopped")
}

// ensureServer 若无运行中的 server 则在本进程内置一个（非 managed，不随 chat 退出）。
// 返回需要在本进程退出时停止的 server（nil 表示复用外部 server）。
func ensureServer() (*server.Server, error) {
	if client.PingServer() == nil {
		return nil, nil
	}
	srv, err := server.NewServer(false)
	if err != nil {
		return nil, err
	}
	if err := srv.Start(); err != nil {
		return nil, err
	}
	log.Println("cata schedule: embedded cata server started")
	return srv, nil
}

// ensureServerRunning 到点执行前确保 server 可用（外部 managed server 可能已退出）。
func ensureServerRunning() error {
	if client.PingServer() == nil {
		return nil
	}
	if _, err := ensureServer(); err != nil {
		return err
	}
	return nil
}

func stopServer(srv *server.Server) {
	if srv == nil {
		return
	}
	srv.Stop()
}
