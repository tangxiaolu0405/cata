package server

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
	"cata/internal/cata/evolve"
	"cata/internal/mcp"
)

// Options server 启动选项。
type Options struct {
	// Managed true：由 cata chat 自动拉起，最后一个客户端断开后退出。
	Managed bool
	// Workspace 非 nil = agent 模式：进程只服务一个工作空间（per-ws socket）。
	Workspace *brain.Workspace
	// SocketPath 非空时使用指定 Unix socket（agent 模式为 ~/.cata/sockets/<ws_id>.sock）。
	SocketPath string
	// IdleTimeout >0 时，**持续该时长没有收到 chat 消息**（无论客户端是否还连着）即自动退出；
	//  0 = 不空闲回收。注册到网关的 keep-alive agent 同样适用——退出后由 supervisor /
	//  本地入口在下一条消息到达时按需拉起（EnsureAgent）。
	IdleTimeout time.Duration
	// KeepAlive agent 由 supervisor 监管（注册到网关）：受 supervisor 存活心跳约束，
	// 但**不再豁免空闲回收**。
	KeepAlive bool
}

// Server 终端 Agent 常驻进程：Unix socket + 流式 LLM 对话 + 可选后台自主演进。
type Server struct {
	socketSrv *SocketServer
	evolve    *evolve.Engine
	ctx       context.Context
	cancel    context.CancelFunc
	managed   bool      // true：由 cata chat 自动拉起，最后一个客户端断开后退出
	startedAt time.Time // 进程启动时间（首次消息诊断展示，便于区分新旧 server）

	workspace   *brain.Workspace // agent 模式绑定的工作空间（nil = 传统多空间模式）
	socketPath  string
	idleTimeout time.Duration
	keepAlive   bool

	// lastActivity 最近一次收到 chat 消息的时间（UnixNano）。空闲回收以**消息**为准，
	// 而非「客户端连接是否存在」——cata chat TUI 会长期挂着一条空闲连接。
	lastActivity atomic.Int64

	// stopOnce 保证 Stop() 只执行一次：多并发调用源（空闲回收 / managed 空连接 / SIGTERM /
	// keep-alive supervisor 心跳）同时触发时，资源只释放一遍（mcp.Shutdown / socket close /
	// cancel），避免重复 close 或日志刷屏。
	stopOnce sync.Once
}

// NewServer 创建传统多空间服务器实例（legacy）。managed 为 true 时无客户端连接后自动停止。
func NewServer(managed bool) (*Server, error) {
	return NewServerWithOptions(Options{Managed: managed})
}

// NewServerWithOptions 创建服务器实例（支持 per-workspace agent 模式）。
func NewServerWithOptions(opts Options) (*Server, error) {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		ctx:         ctx,
		cancel:      cancel,
		managed:     opts.Managed,
		workspace:   opts.Workspace,
		socketPath:  opts.SocketPath,
		idleTimeout: opts.IdleTimeout,
		keepAlive:   opts.KeepAlive,
		startedAt:   time.Now(),
	}, nil
}

// Workspace 返回 agent 模式绑定的工作空间（传统模式为 nil）。
func (s *Server) Workspace() *brain.Workspace { return s.workspace }

// ClientDisconnected 在 socket 客户端断开时调用。
func (s *Server) ClientDisconnected() {
	if s.managed {
		if atomic.LoadInt32(&activeChatStreams) > 0 {
			return
		}
		if s.socketSrv != nil && s.socketSrv.ChatSessions() > 0 {
			return
		}
		log.Println("Managed server: no chat clients, shutting down...")
		go s.Stop()
		return
	}
	// agent 模式的空闲回收由 startIdleReaper 按「最后一条消息」统一计时
	// （客户端断开本身不再是回收依据——断开那一刻可能刚好还有别的工作）。
}

// HasActiveChat 是否仍有活跃 chat 会话（supervisor 心跳据此推断 agent 是否空闲，
// 忙碌时不因 supervisor 短暂失联而自杀）。
func (s *Server) HasActiveChat() bool {
	return s.socketSrv != nil && s.socketSrv.ChatSessions() > 0
}

// touchActivity 记录最后一次 chat 活动（每条 chat 消息到达时调用）。
func (s *Server) touchActivity() {
	s.lastActivity.Store(time.Now().UnixNano())
}

// startIdleReaper 后台按 IdleTimeout 回收进程：
// 距最后一条 chat 消息超过 IdleTimeout、且当前没有进行中的流式轮次（工具执行/生成中
// 不算空闲）时优雅退出。keep-alive（注册到网关）同样适用——下次消息到达时由
// supervisor（机器级隧道）/ 本地入口 EnsureAgent 按需重新拉起。
//
// 背景演进（evolve 周期）**不**算消息，不会阻止回收：无消息的工作空间不再常驻占资源。
func (s *Server) startIdleReaper() {
	if s.idleTimeout <= 0 {
		return
	}
	interval := s.idleTimeout / 4
	if interval < 250*time.Millisecond {
		interval = 250 * time.Millisecond
	}
	if interval > 30*time.Second {
		interval = 30 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				if atomic.LoadInt32(&activeChatStreams) > 0 {
					continue // 有轮次正在执行：不回收
				}
				last := s.lastActivity.Load()
				if last == 0 {
					continue
				}
				idle := time.Since(time.Unix(0, last))
				if idle < s.idleTimeout {
					continue
				}
				log.Printf("agent %s: no message for %s, shutting down (respawned on next message)",
					s.workspaceID(), idle.Round(time.Second))
				s.Stop()
				return
			}
		}
	}()
}

func (s *Server) workspaceID() string {
	if s.workspace != nil {
		return s.workspace.ID
	}
	return "default"
}

// Start 启动 socket 服务。
func (s *Server) Start() error {
	log.Println("Starting Cata server...")
	// 空闲计时从进程启动开始：没有任何消息的 agent 也会被回收。
	s.touchActivity()

	socketSrv, err := NewSocketServerAt(s, s.socketPath)
	if err != nil {
		return err
	}
	s.socketSrv = socketSrv
	socketSrv.Start()
	log.Println("✓ Socket server started")

	if config.Config != nil && config.Config.MCP.Enabled {
		go func() {
			caps := brain.LoadActiveCapabilitiesCached()
			if len(caps.MCP) == 0 {
				log.Println("- MCP: no servers in capabilities (skip warm)")
				return
			}
			log.Println("MCP: background warm (non-blocking)")
			mcp.EnsureInit()
		}()
	} else {
		log.Println("- MCP: disabled or lazy init on full tier")
	}

	if config.Config != nil && config.Config.Evolution.Enabled {
		interval := time.Duration(config.Config.Evolution.CycleInterval) * time.Second
		if interval <= 0 {
			interval = 10 * time.Minute
		}
		// 进程内共享演进引擎单例：chat 触发的 session compress / crystallize 与后台
		// ticker 复用同一实例（指纹/冷却共享、runCycle 互斥），避免重复提炼与并发写。
		s.evolve = evolve.SharedEngine()
		// agent（per-ws 进程）模式：只演进绑定工作空间，绝不遍历全机其它 workspace。
		if s.workspace != nil {
			s.evolve.SetBoundWorkspace(s.workspace)
		}
		s.evolve.Start(s.ctx)
		log.Println("✓ Autonomous evolution started")
	} else {
		log.Println("- Autonomous evolution disabled")
	}

	s.setupSignalHandling()
	s.startIdleReaper()
	if config.Config != nil && !config.Config.Exec.Enabled {
		log.Println("WARNING: exec.enabled=false — terminal run_command disabled until config is updated")
	}
	if s.workspace != nil {
		log.Printf("Cata agent ready (workspace=%s, socket=%s, keep_alive=%t)", s.workspace.ID, s.socketPath, s.keepAlive)
	} else if s.managed {
		log.Println("Cata server ready (managed: exits when last chat disconnects)")
	} else {
		log.Println("Cata server ready (terminal chat: cata / cata chat)")
	}
	return nil
}

func (s *Server) setupSignalHandling() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigChan
		log.Printf("Received signal: %v, shutting down...", sig)
		s.Stop()
	}()
}

// Stop 优雅停止（幂等：多次/并发调用只执行一次）。
func (s *Server) Stop() {
	s.stopOnce.Do(func() {
		mcp.Shutdown()
		s.cancel()
		if s.socketSrv != nil {
			s.socketSrv.Stop()
		}
		time.Sleep(100 * time.Millisecond)
		log.Println("Server stopped")
	})
}

// Wait 阻塞直到收到停止信号。
func (s *Server) Wait() {
	<-s.ctx.Done()
}
