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
	cancel     context.CancelFunc // shutdown 控制命令触发，级联停掉全部 agent
}

// NewSupervisor 创建 supervisor 实例。
func NewSupervisor() *Supervisor {
	return &Supervisor{
		socketPath: config.SupervisorSocketPath(),
		backoff:    newEnsureBackoff(),
	}
}

// RunSupervisor 前台运行 supervisor 守护（cata supervisor）。
//   - 监听 supervisor.sock 控制接口（ensure/stop/list/status/ping/add）
//   - 持有到网关的**机器级隧道**（每机器一条，见 supervisor_tunnel.go）：gateway
//     要某工作空间的连接时按需拉起对应 agent 再转发
//   - 每 30s 复查 link.json（清单变化经隧道推送给网关）+ 截断超大运行日志
//
// 注意：**不再**周期性无条件补拉全部注册 agent——agent 在空闲（默认 30 分钟无消息）
// 后自行退出，仅在消息到达（本地 chat/gateway 或经机器级隧道）时按需拉起。
//
// 阻塞直到 ctx 取消或收到 SIGINT/SIGTERM。
func RunSupervisor(ctx context.Context) error {
	// 守护化后 stdout/stderr 为 nil：日志必须落盘，否则排查问题只能靠猜。
	redirectSupervisorLogs()

	s := NewSupervisor()

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
	s.cancel = cancel
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

	// 机器级隧道：让云端 gateway 在 agent 休眠时仍能送下消息（按需唤醒）。
	// 未配网关（还没 join）时隧道侧自己等配置出现，不报错、不影响本地按需拉起。
	go func() {
		if err := RunSupervisorTunnel(ctx, s.backoff); err != nil {
			log.Printf("cata supervisor: machine tunnel stopped: %v", err)
		}
	}()

	// 定时维护复查
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	tickCount := 0
	for {
		select {
		case <-ctx.Done():
			// 级联关闭：supervisor 退出时把 agent 一起停掉，
			// 避免留下脱离监管的进程继续占资源。
			s.stopAllAgents()
			log.Println("cata supervisor: stopped (all agents stopped)")
			return nil
		case <-ticker.C:
			// 自动接入本机新增工作空间（git/marked），使云端不必逐个手动注册。
			// 只写 link.json，**不**预启动 agent——首个消息到达时才拉起。
			if cfg, err := LoadConfig(); err == nil {
				if err := autoLinkExistingWorkspaces(cfg); err != nil {
					log.Printf("supervisor: auto-link: %v", err)
				}
			}
			// 每 10 分钟（20 tick）截断一次超大的运行日志，防止 ~/.cata 膨胀。
			tickCount++
			if tickCount%20 == 0 {
				config.RotateRuntimeLogs()
			}
		}
	}
}

// stopAllAgents 停止所有注册的 agent 进程（读 pid 文件逐个 SIGTERM）。
// supervisor 退出（SIGTERM/SIGINT）时调用，实现「关 supervisor 即关全部 cata」。
func (s *Supervisor) stopAllAgents() {
	cfg, err := LoadConfig()
	if err != nil {
		log.Printf("cata supervisor: stop all: %v", err)
		return
	}
	for _, id := range cfg.LinkedAgentIDs() {
		if err := killAgentProcess(id); err != nil {
			// 进程可能本来就停了/pid 文件缺失，不阻断其它 agent。
			log.Printf("cata supervisor: stop agent %s: %v", id, err)
			continue
		}
		log.Printf("cata supervisor: stopped agent %s", id)
	}
}

// autoLinkExistingWorkspaces 扫描 home 脑子格下的工作空间，把尚未注册到 link.json 的
// git/marked 项目自动 Add（keep-alive），使本机已有项目自动接入 gateway，避免逐个手动接入。
//
// 过滤规则（与 docs/tunnel.md「自动接入」一致）：
//   - 仅接入 kind ∈ {git, marked}：ephemeral（临时目录/一次性会话）不常驻、不接入
//   - 跳过 home 目录 / CATA_HOME 作为 root_path 的格子（防 agent 绑定敏感目录）
//   - 跳过 root_path 空 / 目录不存在的格子
//   - 已在 link.json 的不重复 Add（幂等）
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
		// 仅自动接入 git/marked 项目；ephemeral 临时工作区不常驻。
		if w.Kind != brain.KindGit && w.Kind != brain.KindMarked {
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
