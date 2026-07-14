package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"cata/internal/cata/config"
	"cata/internal/gateway"
	"cata/internal/gateway/qq"
	"cata/internal/gateway/telegram"
)

func main() {
	if err := config.InitBrainPath(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	args := os.Args[1:]
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
	fmt.Println("cata-gateway — channel adapters for cata server")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  cata-gateway              Start enabled channels (telegram and/or qq)")
	fmt.Println("  cata-gateway run          Same as default")
	fmt.Println("  cata-gateway telegram     Telegram only")
	fmt.Println("  cata-gateway qq           QQ WebSocket only (experimental)")
	fmt.Println("  cata-gateway init         Create ~/.cata/gateway.json from template")
	fmt.Println()
	fmt.Println("Channels (credential-driven, can run together):")
	fmt.Println("  telegram  telegram_bot_token / TELEGRAM_BOT_TOKEN")
	fmt.Println("  qq        qq_app_id + qq_app_secret / QQ_APP_ID + QQ_APP_SECRET")
	fmt.Println("            (WebSocket trial; if QQ disabled WS, QQ channel will fail)")
	fmt.Println()
	fmt.Println("Editions (gateway.json edition field):")
	fmt.Println("  base     Gateway + local cata server (auto_start)")
	fmt.Println("  channel  Gateway only; run cata run separately")
	fmt.Println()
	fmt.Println("Environment:")
	fmt.Println("  CATA_GATEWAY_EDITION    base | channel")
	fmt.Println("  TELEGRAM_BOT_TOKEN / QQ_APP_ID / QQ_APP_SECRET / QQ_SANDBOX")
	fmt.Println("  TELEGRAM_ALLOWED_USERS / QQ_ALLOWED_OPENIDS")
	fmt.Println("  CATA_WORKER_ROOT / CATA_SOCKET / CATA_BIN")
	fmt.Println()
	fmt.Println("Config: ~/.cata/gateway.json  or docs/gateway-config.html")
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
			fmt.Println("Usage: cata-gateway init [--edition base|channel] [--force]")
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
	fmt.Println("Edit telegram_bot_token and/or qq_app_id+qq_app_secret, then: cata-gateway")
}

// runGateway only="" starts all enabled channels; otherwise only that name.
func runGateway(only string) {
	cfg, err := gateway.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	gateway.SetupLogging()
	log.Printf("cata-gateway: edition=%s", cfg.EditionLabel())

	wantTG := cfg.TelegramEnabled()
	wantQQ := cfg.QQEnabled()
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
		if !wantTG && !wantQQ {
			fmt.Fprintf(os.Stderr, "no channel enabled: set telegram_bot_token and/or qq_app_id+qq_app_secret\n")
			os.Exit(1)
		}
	}

	srvMgr := gateway.NewServerManager(cfg)
	if err := srvMgr.Ensure(); err != nil {
		fmt.Fprintf(os.Stderr, "cata server: %v\n", err)
		os.Exit(1)
	}
	defer srvMgr.Stop()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	if wantTG {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bot := telegram.NewBot(cfg)
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
			if err := bot.Run(ctx); err != nil && ctx.Err() == nil {
				log.Printf("qq channel stopped: %v", err)
				errCh <- fmt.Errorf("qq: %w", err)
			}
		}()
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
