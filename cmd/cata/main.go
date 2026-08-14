package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cata/internal/cata/brain"
	"cata/internal/cata/client"
	"cata/internal/cata/clock"
	"cata/internal/cata/config"
	"cata/internal/cata/server"
	"cata/internal/cata/update"
	"cata/internal/cata/version"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "version", "--version", "-V":
			fmt.Printf("cata %s\n", version.Version)
			return
		}
	}

	if err := config.InitBrainPath(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to initialize brain path: %v\n", err)
		os.Exit(1)
	}

	if len(args) == 0 {
		client.RunChat(client.ChatOptions{})
		return
	}

	switch args[0] {
	case "help", "--help", "-h":
		printUsage()
	case "chat":
		client.RunChat(client.ParseChatOptions(args[1:]))
	case "init":
		runInit()
	case "initconfig":
		runInitConfig()
	case "config":
		handleConfigCommand(args[1:])
	case "run":
		runServer(args[1:])
	case "agent":
		runAgent(args[1:])
	case "link":
		runLink(args[1:])
	case "supervisor":
		runSupervisor(args[1:])
	case "schedule":
		runSchedule(args[1:])
	case "update":
		runUpdate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Error: Unknown command: %s\n\n", args[0])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Cata — terminal agent (one binary: server + chat client)")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  cata                    Start chat (default, TUI)")
	fmt.Println("  cata chat [--dir <path>] [--quiet|-q] [--verbose|-v] [--show-thinking]  Start chat at output dir")
	fmt.Println("  cata run                Start server (one per machine; foreground, legacy)")
	fmt.Println("  cata agent              Start one agent per workspace (one LLM loop; --workspace <ws_id>)")
	fmt.Println("  cata link               Register local workspaces to a remote gateway (add/remove/list)")
	fmt.Println("  cata supervisor         Per-machine daemon: keep registered agent processes alive")
	fmt.Println("  cata schedule           Self-hosted scheduler daemon (discovers tasks, fires as real client)")
	fmt.Println("  cata init               Initialize ~/.cata brain layout（不写 config.json）")
	fmt.Println("  cata initconfig         Seed/refresh config.json defaults（保留未知顶层键）")
	fmt.Println("  cata config             Manage configuration")
	fmt.Println("  cata version            Print version")
	fmt.Println("  cata update [--check|--force]  Update from GitHub Releases")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  cata")
	fmt.Println("  cata chat --dir ~/project")
	fmt.Println("  cata chat --dir ~/a --dir ~/b")
	fmt.Println("  cata chat --quiet          # 工具输出静默，只看结论")
	fmt.Println("  cata chat --verbose        # 工具输出完整显示")
	fmt.Println("  cata chat --show-thinking   # 展示模型推理过程（thinking 块）")
	fmt.Println("  cata agent --workspace <ws_id>")
	fmt.Println("  cata agent --workspace <ws_id> --link   # also hold WSS tunnel to gateway")
	fmt.Println("  cata link add --dir ~/project           # register + keep-alive agent")
	fmt.Println("  cata update")
	fmt.Println("  cata update --check")
	fmt.Println()
	fmt.Println("Same output directory: second `cata` exits with an error.")
	fmt.Println("See README.md and agents.md")
}

func runUpdate(args []string) {
	opts := update.Options{}
	for _, a := range args {
		switch a {
		case "--check":
			opts.CheckOnly = true
		case "--force":
			opts.Force = true
		case "-h", "--help":
			fmt.Println("Usage: cata update [--check] [--force]")
			fmt.Println()
			fmt.Println("  --check   Check for a newer release without downloading")
			fmt.Println("  --force   Reinstall even if versions match")
			fmt.Println()
			fmt.Println("Env: CATA_REPO (default tangxiaolu0405/cata), GITHUB_TOKEN (optional)")
			return
		default:
			fmt.Fprintf(os.Stderr, "Error: unknown update flag: %s\n", a)
			os.Exit(1)
		}
	}
	if err := update.Run(opts); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runInit() {
	if err := brain.InitDirectory(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Brain initialized: %s\n", config.CataHome())
	fmt.Println("Config untouched. Seed defaults with: cata initconfig")
	fmt.Println("Next: cata")
}

// runInitConfig 写入 config.json 默认项。未知顶层键（如 llm_ds / llm_previous_qwen）会保留。
func runInitConfig() {
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	const defaultEvolutionInterval = 600
	cfg.Evolution.Enabled = true
	if cfg.Evolution.CycleInterval <= 0 {
		cfg.Evolution.CycleInterval = defaultEvolutionInterval
	}
	cfg.Exec.Enabled = true
	if len(cfg.Exec.Whitelist) == 0 {
		cfg.Exec.Whitelist = []string{"*"}
	}

	configPath := config.GetConfigPath()
	created := false
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		created = true
	}
	if err := config.SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to save config file: %v\n", err)
		os.Exit(1)
	}
	if created {
		fmt.Printf("Configuration file created: %s\n", configPath)
	} else {
		fmt.Printf("Configuration file updated: %s\n", configPath)
	}
	fmt.Printf("Config: llm=%s evolution=%ds exec=%v\n",
		cfg.LLM.Provider, cfg.Evolution.CycleInterval, cfg.Exec.Enabled)
}

// runAgent 启动「一个工作空间一个 agent」进程：绑定单一工作空间（一个 LLM loop），
// 服务 per-ws Unix socket（~/.cata/sockets/<ws_id>.sock）。进程生命周期内不再跨工作空间切换，
// 从结构上消除多空间并行时的全局状态串扰。
//   - --workspace <ws_id>  必需：要服务的工作空间 id
//   - --idle-timeout <s>   无 chat 会话持续该秒数后自动退出（默认 300；0 表示不空闲回收）
//   - --keep-alive         常驻（注册到网关的项目）：不因空闲退出
//   - --link               额外持有到网关的 WSS 隧道（需 link.json 配置 gateway_url/token）
//
// 由 cata chat（本地按需拉起）、cata supervisor（注册常驻）或 cata link（隧道）管理生命周期。
func runAgent(args []string) {
	wsID := ""
	idleTimeout := 300
	keepAlive := false
	link := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--workspace" && i+1 < len(args):
			wsID = args[i+1]
			i++
		case a == "--idle-timeout" && i+1 < len(args):
			if v, err := strconv.Atoi(args[i+1]); err == nil && v >= 0 {
				idleTimeout = v
				i++
			}
		case a == "--keep-alive":
			keepAlive = true
		case a == "--link":
			link = true
		case a == "--managed":
			// 兼容：agent 生命周期由 supervisor/link/chat 管理，非传统 managed server。
		case a == "-h" || a == "--help":
			fmt.Println("Usage: cata agent --workspace <ws_id> [--idle-timeout <sec>] [--keep-alive] [--link]")
			fmt.Println()
			fmt.Println("  One workspace = one agent = one LLM loop, bound to ~/.cata/sockets/<ws_id>.sock.")
			fmt.Println("  --keep-alive  resident (registered to gateway); --link also holds WSS tunnel.")
			return
		default:
			fmt.Fprintf(os.Stderr, "cata agent: unknown flag %q\n", a)
			os.Exit(2)
		}
	}
	if strings.TrimSpace(wsID) == "" {
		fmt.Fprintln(os.Stderr, "cata agent: --workspace <ws_id> required")
		os.Exit(2)
	}

	ws, err := brain.BindWorkspace(wsID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cata agent: bind workspace: %v\n", err)
		os.Exit(1)
	}

	if err := brain.ArchiveSessionLogs(); err != nil {
		log.Printf("cata agent: archive logs: %v", err)
	}
	if keepAlive {
		server.SetupAgentLogging(ws.ID)
	} else {
		server.SetupProcessLogging(false)
	}

	socketPath := config.ResolvedAgentSocketPath(ws.ID)
	srv, err := server.NewServerWithOptions(server.Options{
		Workspace:   ws,
		SocketPath:  socketPath,
		IdleTimeout: time.Duration(idleTimeout) * time.Second,
		KeepAlive:   keepAlive,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cata agent: create server: %v\n", err)
		os.Exit(1)
	}

	if err := srv.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "cata agent: start: %v\n", err)
		os.Exit(1)
	}

	// pid 文件：供 supervisor / `cata link remove` 停止进程。
	if err := writeAgentPID(ws.ID); err != nil {
		log.Printf("cata agent: write pid: %v", err)
	}
	defer removeAgentPID(ws.ID)

	if link {
		// 隧道是长连接（断线自动重连）：放后台 goroutine，主流程走 srv.Wait()。
		// 进程退出（Stop/信号）时随进程结束；断线重连不影响 chat socket 服务。
		go func() {
			if err := startAgentTunnel(ws.ID); err != nil {
				log.Printf("cata agent: tunnel: %v", err)
			}
		}()
	}

	srv.Wait()
}

func writeAgentPID(agentID string) error {
	path := config.AgentPIDPath(agentID)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644)
}

func removeAgentPID(agentID string) {
	_ = os.Remove(config.AgentPIDPath(agentID))
}

func runServer(args []string) {
	managed := false
	for _, a := range args {
		if a == "--managed" {
			managed = true
			break
		}
	}

	srv, err := server.NewServer(managed)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create server: %v\n", err)
		os.Exit(1)
	}

	if err := brain.ArchiveSessionLogs(); err != nil {
		fmt.Fprintf(os.Stderr, "cata: archive logs: %v\n", err)
	}
	server.SetupProcessLogging(managed)

	if err := srv.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start server: %v\n", err)
		os.Exit(1)
	}

	srv.Wait()
}

func handleConfigCommand(args []string) {
	if len(args) < 1 {
		printConfigUsage()
		os.Exit(1)
	}

	switch args[0] {
	case "show":
		handleConfigShow()
	case "set":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "Error: config set requires key and value\n")
			fmt.Fprintf(os.Stderr, "Usage: cata config set <key> <value>\n")
			os.Exit(1)
		}
		handleConfigSet(args[1], args[2])
	case "get":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Error: config get requires key\n")
			fmt.Fprintf(os.Stderr, "Usage: cata config get <key>\n")
			os.Exit(1)
		}
		handleConfigGet(args[1])
	case "keys":
		handleConfigKeys()
	case "edit":
		handleConfigEdit()
	default:
		fmt.Fprintf(os.Stderr, "Error: Unknown config subcommand: %s\n", args[0])
		printConfigUsage()
		os.Exit(1)
	}
}

func printConfigUsage() {
	fmt.Println("Configuration Management")
	fmt.Println()
	fmt.Println("Usage: cata config <subcommand> [args]")
	fmt.Println()
	fmt.Println("Subcommands:")
	fmt.Println("  show              Show current configuration")
	fmt.Println("  keys              List keys supported by get/set")
	fmt.Println("  get <key>         Get a configuration value")
	fmt.Println("  set <key> <value> Set a configuration value")
	fmt.Println("  edit              Print config file path")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  cata config show")
	fmt.Println("  cata config get mcp.tool_timeout_seconds")
	fmt.Println("  cata config set llm.api_format openai   # or anthropic")
	fmt.Println("  cata config set llm.provider deepseek   # label only")
	fmt.Println("  cata config set mcp.tool_timeout_seconds 300")
	fmt.Println(`  cata config set mcp.browser.args '["-y","@playwright/mcp@0.0.75","--extension"]'`)
	fmt.Println("  # 或环境变量 DEEPSEEK_API_KEY；千问见 config.json 内 llm_previous_qwen")
}

func handleConfigShow() {
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	data, err := json.MarshalIndent(config.RedactConfig(cfg), "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting config: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(string(data))
}

func handleConfigKeys() {
	for _, k := range config.ConfigKeys() {
		fmt.Println(k)
	}
}

func handleConfigGet(key string) {
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	value, ok, err := config.GetKey(cfg, key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: key not found: %s\n", key)
		os.Exit(1)
	}

	fmt.Println(formatConfigValue(value))
}

func handleConfigSet(key, value string) {
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	clockInit, err := config.SetKey(cfg, key, value)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error setting config: %v\n", err)
		os.Exit(1)
	}

	if err := config.SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}
	if clockInit {
		_ = clock.Init(value)
	}

	fmt.Printf("Configuration updated: %s = %s\n", key, value)
}

func handleConfigEdit() {
	fmt.Printf("Config file: %s\n", config.GetConfigPath())
	fmt.Println("Edit manually or use: cata config set <key> <value>")
}

func formatConfigValue(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return fmt.Sprintf("%v", x)
	case int:
		return fmt.Sprintf("%d", x)
	case int64:
		return fmt.Sprintf("%d", x)
	case float64:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", x), "0"), ".")
	default:
		return fmt.Sprintf("%v", x)
	}
}
