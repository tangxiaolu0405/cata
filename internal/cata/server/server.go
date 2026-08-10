package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
	"cata/internal/cata/evolve"
	"cata/internal/mcp"
)

// Server 终端 Agent 常驻进程：Unix socket + 流式 LLM 对话 + 可选后台自主演进。
type Server struct {
	socketSrv *SocketServer
	evolve    *evolve.Engine
	ctx       context.Context
	cancel    context.CancelFunc
	managed   bool      // true：由 cata chat 自动拉起，最后一个客户端断开后退出
	startedAt time.Time // 进程启动时间（首次消息诊断展示，便于区分新旧 server）
}

// NewServer 创建服务器实例。managed 为 true 时无客户端连接后自动停止。
func NewServer(managed bool) (*Server, error) {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{ctx: ctx, cancel: cancel, managed: managed, startedAt: time.Now()}, nil
}

// ClientDisconnected 在 socket 客户端断开时调用。
func (s *Server) ClientDisconnected() {
	if !s.managed {
		return
	}
	if atomic.LoadInt32(&activeChatStreams) > 0 {
		return
	}
	if s.socketSrv != nil && s.socketSrv.ChatSessions() > 0 {
		return
	}
	log.Println("Managed server: no chat clients, shutting down...")
	go s.Stop()
}

// Start 启动 socket 服务。
func (s *Server) Start() error {
	log.Println("Starting Cata server...")

	socketSrv, err := NewSocketServer(s)
	if err != nil {
		return fmt.Errorf("create socket server: %w", err)
	}
	s.socketSrv = socketSrv
	socketSrv.Start()
	log.Println("✓ Socket server started")

	if config.Config != nil && config.Config.MCP.Enabled {
		go func() {
			// 仅当当前 mode 的 capabilities 声明了 mcp 时才预热；browser 非必须。
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
	if s.managed {
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
