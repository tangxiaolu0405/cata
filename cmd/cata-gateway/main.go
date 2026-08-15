package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"cata/internal/cata/config"
	"cata/internal/cata/version"
	"cata/internal/gateway"
	"cata/internal/gateway/qq"
	"cata/internal/gateway/telegram"
	"cata/internal/gateway/tunnel"
	"cata/internal/gateway/ui"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "version", "--version", "-V":
			fmt.Printf("cata-gateway %s\n", version.Version)
			return
		}
	}

	if err := config.InitBrainPath(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(args) == 0 || args[0] == "run" {
		runGateway("")
		return
	}
	switch args[0] {
	case "telegram":
		runGateway("telegram")
	case "qq":
		runGateway("qq")
	case "help", "--help", "-h":
		printUsage()
	case "init":
		runInit(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", args[0])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("cata-gateway — Web UI + channel adapters + remote agent tunnel (cata)")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  cata-gateway              Start UI (default) + enabled channels")
	fmt.Println("  cata-gateway run          Same as default")
	fmt.Println("  cata-gateway telegram     Telegram only (UI still if enabled)")
	fmt.Println("  cata-gateway qq           QQ WebSocket only (experimental; UI if enabled)")
	fmt.Println("  cata-gateway init         Create ~/.cata/gateway.json from template")
	fmt.Println("  cata-gateway version      Print version")
	fmt.Println()
	fmt.Println("Web UI (default http://0.0.0.0:8787, phone: http://<本机局域网IP>:8787):")
	fmt.Println("  Multi-project chat (cwd = real project path); channels panel is read-only")
	fmt.Println("  ui_listen in gateway.json; CATA_GATEWAY_UI=0 / off disables UI")
	fmt.Println("  UI-only works without Telegram/QQ credentials")
	fmt.Println()
	fmt.Println("Remote mode (cata_server.mode=remote; cloud gateway):")
	fmt.Println("  Accepts WSS tunnels from `cata agent --link` on any machine; project = online agent")
	fmt.Println("  gateway_token (or CATA_GATEWAY_TOKEN) required; tunnel_listen default 0.0.0.0:8799")
	fmt.Println("  Worker side: cata link add --dir <path> --gateway <url> --token <token>")
	fmt.Println()
	fmt.Println("Channels (credential-driven, can run together):")
	fmt.Println("  telegram  telegram_bot_token / TELEGRAM_BOT_TOKEN")
	fmt.Println("  qq        qq_app_id + qq_app_secret / QQ_APP_ID + QQ_APP_SECRET")
	fmt.Println("            (WebSocket trial; if QQ disabled WS, QQ channel will fail)")
	fmt.Println()
	fmt.Println("Editions (gateway.json edition field):")
	fmt.Println("  base     Gateway + local cata server (auto_start)")
	fmt.Println("  channel  Gateway only; run cata run separately")
	fmt.Println("  remote   Cloud registry + tunnel routing (cata_server.mode=remote)")
	fmt.Println()
	fmt.Println("Environment:")
	fmt.Println("  CATA_GATEWAY_EDITION    base | channel")
	fmt.Println("  CATA_GATEWAY_UI         listen addr, or 0/off to disable")
	fmt.Println("  CATA_GATEWAY_TOKEN / CATA_TUNNEL_LISTEN / CATA_GATEWAY_ALLOW_AGENTS / CATA_GATEWAY_DEFAULT_AGENT")
	fmt.Println("  TELEGRAM_BOT_TOKEN / QQ_APP_ID / QQ_APP_SECRET / QQ_SANDBOX")
	fmt.Println("  TELEGRAM_ALLOWED_USERS / QQ_ALLOWED_OPENIDS")
	fmt.Println("  CATA_WORKER_ROOT / CATA_SOCKET / CATA_BIN")
	fmt.Println()
	fmt.Println("Config: ~/.cata/gateway.json  or docs/gateway-config.html")
	fmt.Println("Docs: docs/gateway.md, docs/tunnel.md")
	fmt.Println("Log: ~/.cata/cata-gateway.log")
}

func runInit(args []string) {
	edition := gateway.EditionBase
	force := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--edition", "-e":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "missing value for %s\n", args[i])
				os.Exit(1)
			}
			i++
			edition = args[i]
		case "--force", "-f":
			force = true
		case "help", "--help", "-h":
			fmt.Println("Usage: cata-gateway init [--edition base|channel|remote] [--force]")
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown flag: %s\n", args[i])
			os.Exit(1)
		}
	}
	path, err := gateway.InitConfig(gateway.InitOptions{Edition: edition, Force: force})
	if err != nil {
		fmt.Fprintf(os.Stderr, "init failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Gateway config: %s\n", path)
	fmt.Println("Local UI defaults to http://0.0.0.0:8787 (ui_listen / CATA_GATEWAY_UI).")
	fmt.Println("On phone use http://<this-machine-LAN-IP>:8787. Add projects in the UI or gateway.json projects[]. Edit channel tokens as needed, then: cata-gateway")
}

func channelTelegramReady(cfg gateway.Config) bool {
	t := strings.TrimSpace(cfg.TelegramBotToken)
	return t != "" && t != "YOUR_BOT_TOKEN"
}

func channelQQReady(cfg gateway.Config) bool {
	id := strings.TrimSpace(cfg.QQAppID)
	sec := strings.TrimSpace(cfg.QQAppSecret)
	if id == "" || sec == "" {
		return false
	}
	if id == "YOUR_QQ_APP_ID" || sec == "YOUR_QQ_APP_SECRET" {
		return false
	}
	return true
}

// runGateway only="" starts all enabled channels; otherwise only that name.
// Local UI starts whenever ui_listen is enabled (independent of channels).
func runGateway(only string) {
	cfg, err := gateway.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	gateway.SetupLogging()
	log.Printf("cata-gateway: edition=%s", cfg.EditionLabel())

	remote := cfg.RemoteMode()
	var reg *tunnel.Registry
	var machines *tunnel.MachinesStore
	var join *tunnel.JoinManager
	if remote {
		if !cfg.TunnelEnabled() {
			fmt.Fprintf(os.Stderr, "remote mode requires gateway_token (env CATA_GATEWAY_TOKEN or gateway.json gateway_token)\n")
			os.Exit(1)
		}
		reg = tunnel.NewRegistry()
		machines = tunnel.NewMachinesStore(tunnel.MachinesPath())
		join = tunnel.NewJoinManager(machines)
		log.Printf("cata-gateway: remote mode: tunnel listen=%s (allow_agents=%d)", cfg.ResolvedTunnelListen(), len(cfg.AllowAgentIDs))
	}

	wantUI := cfg.UIEnabled()
	wantTG := channelTelegramReady(cfg)
	wantQQ := channelQQReady(cfg)
	switch only {
	case "telegram":
		wantQQ = false
		if !wantTG {
			fmt.Fprintf(os.Stderr, "TELEGRAM_BOT_TOKEN / telegram_bot_token required\n")
			os.Exit(1)
		}
	case "qq":
		wantTG = false
		if !wantQQ {
			fmt.Fprintf(os.Stderr, "QQ_APP_ID + QQ_APP_SECRET / qq_app_id + qq_app_secret required\n")
			os.Exit(1)
		}
	default:
		if !remote && !wantTG && !wantQQ && !wantUI {
			fmt.Fprintf(os.Stderr, "nothing to run: enable ui_listen and/or set telegram_bot_token / qq credentials\n")
			fmt.Fprintf(os.Stderr, "Hint: cata-gateway init\n")
			os.Exit(1)
		}
	}

	// remote 模式不拉起本机进程：worker 在各机器上由 `cata agent` 自持隧道。
	// 本地模式改为 per-ws agent：确保 supervisor 守护保活常驻 agent；
	// 未注册项目的 agent 由 DialLocalAgent 按需拉起（不再拉起 legacy cata run）。
	if !remote {
		gateway.EnsureLocalAgents(cfg)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	errCh := make(chan error, 4)

	if remote {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := tunnel.Run(ctx, cfg.ResolvedTunnelListen(), reg, tunnel.HandlerOptions{
				Token:         cfg.GatewayToken,
				AllowAgentIDs: cfg.AllowAgentIDs,
				Machines:      machines,
				Join:          join,
			}); err != nil && ctx.Err() == nil {
				log.Printf("tunnel stopped: %v", err)
				errCh <- fmt.Errorf("tunnel: %w", err)
			}
		}()
	}

	if wantUI {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var srv *ui.Server
			if remote {
				srv = ui.NewServerWithRegistry(cfg, ui.DefaultHub, reg)
			} else {
				srv = ui.NewServer(cfg, ui.DefaultHub)
			}
			if err := srv.Run(ctx); err != nil && ctx.Err() == nil {
				log.Printf("ui stopped: %v", err)
				errCh <- fmt.Errorf("ui: %w", err)
			}
		}()
	} else {
		log.Printf("cata-gateway: local UI disabled")
	}

	if wantTG {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bot := telegram.NewBot(cfg)
			if remote {
				if sessions, err := gateway.RemoteSessionManagerForDefaultAgent(cfg, reg); err == nil {
					bot = telegram.NewBotWithSessions(cfg, sessions)
				} else {
					log.Printf("telegram: remote sessions unavailable: %v", err)
				}
			}
			if err := bot.Run(ctx); err != nil && ctx.Err() == nil {
				log.Printf("telegram channel stopped: %v", err)
				errCh <- fmt.Errorf("telegram: %w", err)
			}
		}()
	}
	if wantQQ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bot := qq.NewBot(cfg)
			if remote {
				if sessions, err := gateway.RemoteSessionManagerForDefaultAgent(cfg, reg); err == nil {
					bot = qq.NewBotWithSessions(cfg, sessions)
				} else {
					log.Printf("qq: remote sessions unavailable: %v", err)
				}
			}
			if err := bot.Run(ctx); err != nil && ctx.Err() == nil {
				log.Printf("qq channel stopped: %v", err)
				errCh <- fmt.Errorf("qq: %w", err)
			}
		}()
	}

	if !remote && !wantTG && !wantQQ {
		log.Printf("cata-gateway: UI-only mode (no Telegram/QQ credentials)")
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		<-done
		return
	case <-done:
	}
	close(errCh)
	var last error
	for err := range errCh {
		last = err
	}
	if last != nil {
		fmt.Fprintf(os.Stderr, "gateway stopped: %v\n", last)
		os.Exit(1)
	}
}
