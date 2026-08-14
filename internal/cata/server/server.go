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
	// IdleTimeout >0 且非 KeepAlive 时，chatSessions==0 持续该时长后自动退出。
	IdleTimeout time.Duration
	// KeepAlive 常驻（注册到网关的项目）：不因空闲退出。
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

	idleMu    sync.Mutex
	idleTimer *time.Timer
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
	// agent 模式：空闲超时回收（仅非 keep-alive）。
	if s.idleTimeout > 0 && !s.keepAlive && s.socketSrv != nil && s.socketSrv.ChatSessions() == 0 {
		s.idleMu.Lock()
		if s.idleTimer == nil {
			s.idleTimer = time.AfterFunc(s.idleTimeout, func() {
				log.Printf("agent %s: idle for %s, shutting down", s.workspaceID(), s.idleTimeout)
				s.Stop()
			})
		}
		s.idleMu.Unlock()
	}
}

// touchActivity 取消空闲回收计时（新 chat 会话开始时调用）。
func (s *Server) touchActivity() {
	if s.idleTimeout <= 0 {
		return
	}
	s.idleMu.Lock()
	if s.idleTimer != nil {
		s.idleTimer.Stop()
		s.idleTimer = nil
	}
	s.idleMu.Unlock()
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
		s.evolve = evolve.NewEngine(interval)
		s.evolve.Start(s.ctx)
		log.Println("✓ Autonomous evolution started")
	} else {
		log.Println("- Autonomous evolution disabled")
	}

	s.setupSignalHandling()
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

// Stop 优雅停止。
func (s *Server) Stop() {
	mcp.Shutdown()
	s.cancel()
	if s.socketSrv != nil {
		s.socketSrv.Stop()
	}
	time.Sleep(100 * time.Millisecond)
	log.Println("Server stopped")
}

// Wait 阻塞直到收到停止信号。
func (s *Server) Wait() {
	<-s.ctx.Done()
}
