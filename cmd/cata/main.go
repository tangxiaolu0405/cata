package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"cata/internal/cata/brain"
	"cata/internal/cata/client"
	"cata/internal/cata/clock"
	"cata/internal/cata/config"
	"cata/internal/cata/link"
	"cata/internal/cata/server"
	"cata/internal/cata/update"
	"cata/internal/cata/version"
	"cata/internal/llm"
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
		runServer(args[1:]) // legacy 支撑命令：供 cata-pet / scheduler 内部拉起，用户一般不直接使用
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
	fmt.Println("  cata run                Start legacy server (internal: cata-pet / scheduler 用; chat 不再依赖)")
	fmt.Println("  cata agent              Start one agent per workspace (one LLM loop; --workspace <ws_id>)")
	fmt.Println("  cata link               Register local workspaces to a remote gateway (add/remove/list)")
	fmt.Println("  cata supervisor [stop]   Per-machine daemon: keep registered agent processes alive; stop 级联关闭全部 agent")
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
	withTunnel := false
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
			withTunnel = true
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

	// 启动自动探测：缺探测/过期的 provider 后台探测并热应用（不阻塞连接）。
	// 探测失败保留既有配置；多进程由文件锁互斥。
	go llm.AutoProbeStartup(true)

	if withTunnel {
		// 隧道是长连接（断线自动重连）：放后台 goroutine，主流程走 srv.Wait()。
		// 进程退出（Stop/信号）时随进程结束；断线重连不影响 chat socket 服务。
		go func() {
			if err := startAgentTunnel(ws.ID); err != nil {
				log.Printf("cata agent: tunnel: %v", err)
			}
		}()
	}

	// 常驻 agent（--keep-alive）依赖 supervisor 保活；supervisor 被 kill（含 SIGKILL）
	// 时 agent 收不到信号（detachCmd 脱离进程组），这里靠控制口心跳自检并优雅退出，
	// 避免 supervisor 死后 agent 变成孤儿继续占资源/持隧道。
	// 但**有活跃 chat 会话时不死机**：Busy 返回 HasActiveChat，待会话结束或在当前
	// 空闲期再收敛，避免正在进行的对话/任务被心跳误杀（EOF 根因）。
	if keepAlive {
		go link.WatchSupervisorAndStop(link.SupervisorWatchConfig{
			Busy: srv.HasActiveChat,
		}, func() {
			srv.Stop()
		})()
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

	// 启动自动探测（agent 路径同样触发；server 进程下也跑一次）。
	go llm.AutoProbeStartup(true)

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
	case "provider":
		handleConfigProvider(args[1:])
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

// handleConfigProvider 多 LLM 提供商子命令：
//
//	cata config provider list               列出已注册 provider 与探测状态
//	cata config provider probe <name>       自动探测该 provider（成功写回，失败保留旧配置）
//	cata config provider switch <name> [model]  切换激活 provider（缺探测自动补探；可指定 model）
func handleConfigProvider(args []string) {
	if len(args) < 1 {
		printProviderUsage()
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		printProviderList()
	case "probe":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Error: provider probe requires <name> (see list)")
			os.Exit(1)
		}
		runProviderProbe(args[1])
	case "switch":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Error: provider switch requires <name> (see list)")
			os.Exit(1)
		}
		model := ""
		if len(args) >= 3 {
			model = args[2]
		}
		runProviderSwitch(args[1], model)
	default:
		fmt.Fprintf(os.Stderr, "Error: Unknown provider subcommand: %s\n", args[0])
		printProviderUsage()
		os.Exit(1)
	}
}

func printProviderUsage() {
	fmt.Println("LLM Provider Management")
	fmt.Println()
	fmt.Println("Usage: cata config provider <subcommand> [args]")
	fmt.Println()
	fmt.Println("Subcommands:")
	fmt.Println("  list                     List registered providers and probe status")
	fmt.Println("  probe <name>             Auto-probe a provider (success writes back; failure keeps config)")
	fmt.Println("  switch <name> [model]    Activate a provider (auto-probes if missing), optionally pick model")
	fmt.Println()
	fmt.Println("Probing is automatic: capabilities are discovered, not hand-written.")
}

func printProviderList() {
	providers, err := config.LoadLLMProviders()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading providers: %v\n", err)
		os.Exit(1)
	}
	var names []string
	for n := range providers.Providers {
		names = append(names, n)
	}
	sort.Strings(names)

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tACTIVE\tAPI_URL\tMODEL\tPROBE")
	for _, n := range names {
		prov := providers.Providers[n]
		active := ""
		if n == providers.Active {
			active = "*"
		}
		probeState := "unprobed"
		if prov.Probe.ProbedAt != "" {
			if prov.Probe.ProbedError != "" {
				probeState = "failed: " + truncate(prov.Probe.ProbedError, 40)
			} else {
				probeState = fmt.Sprintf("ok (%d models, %s)", len(prov.Probe.Models), shortTime(prov.Probe.ProbedAt))
			}
		}
		model := prov.Model
		if model == "" {
			model = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", n, active, prov.APIURL, model, probeState)
	}
	_ = w.Flush()
}

func runProviderProbe(name string) {
	providers, err := config.LoadLLMProviders()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading providers: %v\n", err)
		os.Exit(1)
	}
	prov, ok := providers.Providers[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: provider %q not found (use `cata config provider list`)\n", name)
		os.Exit(1)
	}
	fmt.Printf("Probing %s (%s)…\n", name, prov.APIURL)
	rep, ok := llm.ProbeAndPersist(context.Background(), name, true)
	if !ok {
		// 失败不覆盖：读回最新 ProbedError 说明。
		if again, err := config.LoadLLMProviders(); err == nil {
			if pv, ok2 := again.Providers[name]; ok2 && pv.Probe.ProbedError != "" {
				fmt.Fprintf(os.Stderr, "Probe failed; existing config kept: %s\n", pv.Probe.ProbedError)
				os.Exit(1)
			}
		}
		fmt.Fprintln(os.Stderr, "Probe failed; existing config kept.")
		os.Exit(1)
	}
	fmt.Printf("OK: %d models probed\n", len(rep.Models))
	for _, m := range rep.Models {
		mods := ""
		if c, ok := rep.Capabilities[m]; ok {
			mods = strings.Join(c.Modalities, ",")
		}
		fmt.Printf("  %-40s [%s]\n", m, mods)
	}
}

func runProviderSwitch(name, model string) {
	// 缺探测/过期/上次失败 → 先自动补探（失败保留既有配置，能力表沿用旧值不覆盖）。
	providers, err := config.LoadLLMProviders()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading providers: %v\n", err)
		os.Exit(1)
	}
	prov, ok := providers.Providers[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: provider %q not found (use `cata config provider list`)\n", name)
		os.Exit(1)
	}
	if config.ProviderProbeExpired(prov.Probe.ProbedAt, 0) || prov.Probe.ProbedError != "" {
		fmt.Printf("Auto-probing %s (%s)…\n", name, prov.APIURL)
		if _, ok := llm.ProbeAndPersist(context.Background(), name, true); ok {
			fmt.Println("Probe OK.")
		} else {
			fmt.Println("Probe failed; switching with existing config (capabilities kept).")
		}
	}
	// 可选指定模型：写入 provider 定义后激活。
	if strings.TrimSpace(model) != "" {
		if err := config.SetProviderModel(name, model); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
	if err := config.ActivateProvider(name); err != nil {
		fmt.Fprintf(os.Stderr, "Error switching: %v\n", err)
		os.Exit(1)
	}
	// 展示当前生效条目。
	cfg, err := config.LoadConfig()
	if err == nil {
		fmt.Printf("Active provider: %s\n", name)
		fmt.Printf("  model:      %s\n", cfg.LLM.Model)
		fmt.Printf("  api_url:    %s\n", cfg.LLM.APIURL)
		fmt.Printf("  api_format: %s\n", cfg.LLM.APIFormat)
		if v := cfg.LLM.Models["chat_vision"]; v != "" {
			fmt.Printf("  chat_vision: %s\n", v)
		}
		if len(cfg.LLM.Capabilities) > 0 {
			fmt.Printf("  capabilities: %d model(s) probed\n", len(cfg.LLM.Capabilities))
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func shortTime(rfc string) string {
	ts, err := time.Parse(time.RFC3339, rfc)
	if err != nil {
		return rfc
	}
	return ts.Format("2006-01-02 15:04")
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
	fmt.Println("  provider          Manage LLM providers (list/probe/switch)")
	fmt.Println("  edit              Print config file path")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  cata config show")
	fmt.Println("  cata config get mcp.tool_timeout_seconds")
	fmt.Println("  cata config provider list")
	fmt.Println("  cata config provider probe deepseek")
	fmt.Println("  cata config provider switch deepseek")
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
