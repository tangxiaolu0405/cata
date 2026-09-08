package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"cata/internal/cata/api"
	"cata/internal/cata/brain"
	"cata/internal/cata/config"
	"cata/internal/cata/link"
)

// runServeAPI 启动模式二 HTTP API（cata serve-api）。
//
// 架构：纯 HTTP 网关层，拨目标工作空间的 per-ws agent socket（复用 socket_chat 语义，
// NDJSON over SSE）。agent 未运行时按需拉起（link.EnsureAgent，与 gateway 渠道对称），
// 不内嵌 server，避免与已有 agent/supervisor 冲突。
//
//	--workspace <ws_id>  必需：要服务的工作空间 id
//	--listen <addr>      监听地址（默认 127.0.0.1:8788；公网暴露需显式 0.0.0.0:port + --token）
//	--token <token>      Bearer 认证 token（也支持 CATA_API_TOKEN 环境变量；空 = 仅本机回环）
//	--idle-timeout <s>   无会话持续该秒数后自动退出（默认 0 = 不回收）
func runServeAPI(args []string) {
	wsID := ""
	listen := api.DefaultListen
	token := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--workspace" && i+1 < len(args):
			wsID = args[i+1]
			i++
		case a == "--listen" && i+1 < len(args):
			listen = args[i+1]
			i++
		case a == "--token" && i+1 < len(args):
			token = args[i+1]
			i++
		case a == "-h" || a == "--help":
			printServeAPIUsage()
			return
		default:
			fmt.Fprintf(os.Stderr, "cata serve-api: unknown flag %q\n", a)
			os.Exit(2)
		}
	}
	if strings.TrimSpace(wsID) == "" {
		fmt.Fprintln(os.Stderr, "cata serve-api: --workspace <ws_id> required")
		os.Exit(2)
	}

	// token 优先级：--token > CATA_API_TOKEN。
	if token == "" {
		token = os.Getenv("CATA_API_TOKEN")
	}

	// 校验工作空间存在。
	ws, err := brain.BindWorkspace(wsID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cata serve-api: bind workspace: %v\n", err)
		os.Exit(1)
	}

	// 确保 agent 在跑（复用已有 per-ws socket，未运行则拉起）。
	if err := link.EnsureAgent(wsID); err != nil {
		fmt.Fprintf(os.Stderr, "cata serve-api: ensure agent %s: %v\n", wsID, err)
		os.Exit(1)
	}
	socketPath := config.ResolvedAgentSocketPath(ws.ID)
	log.Printf("cata serve-api: workspace=%s socket=%s", ws.ID, socketPath)

	// HTTP API worker：按需拨目标 agent 的 per-ws socket。
	worker := api.NewWorker(ws.ID, ws.RootPath)
	worker.Dial = func() (net.Conn, error) {
		if err := link.EnsureAgent(wsID); err != nil {
			return nil, err
		}
		return net.DialTimeout("unix", socketPath, 5*time.Second)
	}
	apiSrv := api.NewServer(listen, token, worker)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 信号处理：SIGTERM/SIGINT 优雅退出。
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigChan
		log.Printf("cata serve-api: received %v, shutting down", sig)
		cancel()
	}()

	if err := apiSrv.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "cata serve-api: %v\n", err)
		os.Exit(1)
	}
	apiSrv.Stop()
	worker.CloseAll()
}

func printServeAPIUsage() {
	fmt.Println("Cata HTTP API (mode 2) — expose socket_chat semantics over HTTP/SSE")
	fmt.Println()
	fmt.Println("Usage: cata serve-api --workspace <ws_id> [--listen <addr>] [--token <token>]")
	fmt.Println()
	fmt.Println("  --workspace <ws_id>   Workspace to serve (required)")
	fmt.Println("  --listen <addr>       Listen address (default 127.0.0.1:8788)")
	fmt.Println("  --token <token>       Bearer token (also CATA_API_TOKEN; empty = loopback only)")
	fmt.Println()
	fmt.Println("Endpoints:")
	fmt.Println("  POST /v1/chat    Chat with NDJSON over SSE (token/progress/tool_start/exec_confirm_required/user_choice/done)")
	fmt.Println("  POST /v1/resolve Resolve exec_confirm / user_choice")
	fmt.Println("  GET  /v1/status  Health check + session count")
	fmt.Println("  POST /v1/reset   Reset a session")
	fmt.Println()
	fmt.Println("Example:")
	fmt.Println("  cata serve-api --workspace users-lucas-work-cata --token secret --listen 127.0.0.1:8788")
}