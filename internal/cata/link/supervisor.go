package link

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
)

// Supervisor 每机器一个：只管注册工作空间的 agent 进程生命周期（拉起/保活/停止），
// 不转发对话、不持有隧道（隧道由各 agent 进程自己持有）。
type Supervisor struct {
	socketPath string
	mu         sync.Mutex
	ln         net.Listener
	backoff    *ensureBackoff
}

// NewSupervisor 创建 supervisor 实例。
func NewSupervisor() *Supervisor {
	return &Supervisor{
		socketPath: config.SupervisorSocketPath(),
		backoff:    newEnsureBackoff(),
	}
}

// RunSupervisor 前台运行 supervisor 守护（cata supervisor）。
//   - 启动时确保所有启用+常驻的 agent 在运行
//   - 监听 supervisor.sock 控制接口（ensure/stop/list/status/ping）
//   - 每 30s 复查常驻 agent 并补拉（带失败退避：连续失败暂停该 agent）
//
// 阻塞直到 ctx 取消或收到 SIGINT/SIGTERM。
func RunSupervisor(ctx context.Context) error {
	// 守护化后 stdout/stderr 为 nil：日志必须落盘，否则排查问题只能靠猜。
	redirectSupervisorLogs()

	s := NewSupervisor()
	if err := s.ensureAll(); err != nil {
		log.Printf("cata supervisor: initial ensure: %v", err)
	}

	// 控制 socket 单例：已有 supervisor 在跑则退出。
	ln, acquired, err := acquireSupervisorLock(s.socketPath)
	if err != nil {
		return err
	}
	if !acquired {
		return fmt.Errorf("cata supervisor already running (%s)", s.socketPath)
	}
	defer ln.Close()
	defer os.Remove(s.socketPath)
	s.ln = ln

	log.Printf("cata supervisor: control socket %s", s.socketPath)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		select {
		case <-sig:
			cancel()
		case <-ctx.Done():
		}
	}()

	// 控制接口
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					continue
				}
			}
			go s.handleConn(conn)
		}
	}()

	// 常驻保活复查
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	tickCount := 0
	for {
		select {
		case <-ctx.Done():
			log.Println("cata supervisor: stopped")
			return nil
		case <-ticker.C:
			if err := s.ensureAll(); err != nil {
				log.Printf("cata supervisor: ensure all: %v", err)
			}
			// 每 10 分钟（20 tick）截断一次超大的运行日志，防止 ~/.cata 膨胀。
			tickCount++
			if tickCount%20 == 0 {
				config.RotateRuntimeLogs()
			}
		}
	}
}

func (s *Supervisor) ensureAll() error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	// 自动接入本机已有工作空间（git/marked），避免用户逐个手动 link add。
	if err := autoLinkExistingWorkspaces(cfg); err != nil {
		log.Printf("supervisor: auto-link: %v", err)
		cfg, err = LoadConfig() // 重载，纳入刚自动注册的 agent
		if err != nil {
			return err
		}
	}
	ids := cfg.LinkedAgentIDs()
	for _, id := range ids {
		e := cfg.Agents[id]
		if !e.Enabled {
			continue
		}
		// 带崩溃退避：单个失败不阻断其它 agent，连续失败暂停补拉。
		if err := s.backoff.ensure(id); err != nil {
			log.Printf("supervisor: ensure agent %s: %v", id, err)
			continue
		}
	}
	return nil
}

// autoLinkExistingWorkspaces 扫描 ~/.cata/brain/workspaces 下的所有工作空间
// （新旧项目都算，跳过 .cata_worker 渠道沙箱），把尚未注册到 link.json 的自动
// Add（keep-alive），使本机已有项目自动接入 gateway，避免逐个手动接入。
func autoLinkExistingWorkspaces(cfg Config) error {
	wsList, err := brain.ListHomeWorkspaces()
	if err != nil {
		return err
	}
	linked := cfg.Agents
	added := 0
	for _, w := range wsList {
		if w.ID == "" || w.RootPath == "" {
			continue
		}
		if isHomeRootPath(w.RootPath) {
			log.Printf("supervisor: auto-link skip %s (root_path is home dir)", w.ID)
			continue
		}
		if _, exists := linked[w.ID]; exists {
			continue
		}
		if _, err := Add(w.RootPath, true); err != nil {
			log.Printf("supervisor: auto-link %s: %v", w.ID, err)
			continue
		}
		added++
	}
	if added > 0 {
		log.Printf("supervisor: auto-linked %d existing workspace(s)", added)
	}
	return nil
}
