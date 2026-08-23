package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"cata/internal/cata/link"
)

// runLink 管理「本地工作空间 → 远程网关」注册：
//
//	cata link join <gateway_url>             # 机器首次接入：join 拿逐机器 token
//	cata link add --dir <path>              # 注册工作空间（join 后）
//	cata link remove <agent_id>
//	cata link list
//	cata link status
//
// 注册（add）即常驻：写入 link.json 并拉起/保活该工作空间的 agent 进程。
func runLink(args []string) {
	if len(args) == 0 {
		printLinkUsage()
		os.Exit(1)
	}
	switch args[0] {
	case "join":
		runLinkJoin(args[1:])
	case "add":
		runLinkAdd(args[1:])
	case "remove", "rm":
		runLinkRemove(args[1:])
	case "list", "ls":
		runLinkList()
	case "status":
		runLinkStatus()
	case "help", "--help", "-h":
		printLinkUsage()
	default:
		fmt.Fprintf(os.Stderr, "cata link: unknown subcommand %q\n", args[0])
		printLinkUsage()
		os.Exit(1)
	}
}

func printLinkUsage() {
	fmt.Println("Usage:")
	fmt.Println("  cata link join <gateway_url>")
	fmt.Println("  cata link add --dir <path>")
	fmt.Println("  cata link remove <agent_id>")
	fmt.Println("  cata link list")
	fmt.Println("  cata link status")
	fmt.Println()
	fmt.Println("Register a local workspace to a remote cata-gateway. agent_id = workspace id.")
	fmt.Println("First join the gateway (one-time) to get a per-machine token; then add workspaces.")
	fmt.Println("Registered workspaces run a keep-alive `cata agent` and hold a WSS tunnel to the gateway.")
	fmt.Println()
	fmt.Println("Config: ~/.cata/link.json  (gateway_url / machine_id / machine_token / workspace_root / agents)")
}

// runLinkJoin 机器首次接入网关：join 拿逐机器 token。
// 用法：cata link join <gateway_url> [--token <legacy>]
// gateway_url 作为位置参数（join 本身就表达「加入某网关」），无需 --gateway 前缀。
// 无需任何固定口令：握手靠自定义协议头 X-Cata-Join，授权靠一次性 code + 网关 UI 批准，
// 最终凭证为网关签发的逐机器 token（machine_token）。--token 仍可选（传递则写入 link.json 兼容字段，但不再用于鉴权）。
// 兼容：仍接受 --gateway <url> 写法（deprecated）。
func runLinkJoin(args []string) {
	gatewayURL := ""
	gatewayToken := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--gateway" && i+1 < len(args): // deprecated：用位置参数
			gatewayURL = args[i+1]
			i++
		case a == "--token" && i+1 < len(args):
			gatewayToken = args[i+1]
			i++
		case a == "-h" || a == "--help":
			fmt.Println("Usage: cata link join <gateway_url> [--token <gateway_token>]")
			fmt.Println("  gateway_url 为位置参数；gateway_token 可选（不再用于鉴权），join 靠 X-Cata-Join 协议头 + UI 批准。")
			fmt.Println("  兼容旧写法：cata link join --gateway <url>")
			return
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "cata link join: unknown flag %q\n", a)
			os.Exit(2)
		default:
			// 位置参数 = gateway url。
			if gatewayURL != "" {
				fmt.Fprintf(os.Stderr, "cata link join: unexpected extra argument %q\n", a)
				os.Exit(2)
			}
			gatewayURL = a
		}
	}
	if strings.TrimSpace(gatewayURL) == "" {
		fmt.Fprintln(os.Stderr, "cata link join: <gateway_url> required (e.g. cata link join http://gw.example.com:8787)")
		os.Exit(2)
	}
	res, err := link.Join(gatewayURL, gatewayToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cata link join: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("joined: machine_id=%s\n", res.MachineID)

	// 自动拉起 supervisor：其启动 ensure 会扫描本机已有工作空间并自动 link add，
	// 无需再手动逐个 `cata link add --dir <path>`。
	if err := link.EnsureSupervisorDaemon(); err != nil {
		fmt.Printf("warning: supervisor start: %v\n（可稍后手动运行 cata supervisor，或 cata link add --dir <path> 接入指定工作空间）\n", err)
		return
	}
	fmt.Println("已自动拉起 supervisor，正在自动接入本机已有工作空间…")
	fmt.Println("查看: cata link list   |   停止全部: pkill -f 'cata supervisor' 或 kill <supervisor_pid>")
}

func runLinkAdd(args []string) {
	dir := ""
	keepAlive := true
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--dir" && i+1 < len(args):
			dir = args[i+1]
			i++
		case a == "--no-keep-alive":
			keepAlive = false
		case a == "-h" || a == "--help":
			fmt.Println("Usage: cata link add --dir <path> [--no-keep-alive]")
			fmt.Println("Requires a prior `cata link join` (per-machine token).")
			return
		default:
			fmt.Fprintf(os.Stderr, "cata link add: unknown flag %q\n", a)
			os.Exit(2)
		}
	}
	if strings.TrimSpace(dir) == "" {
		fmt.Fprintln(os.Stderr, "cata link add: --dir <path> required")
		os.Exit(2)
	}

	entry, err := link.Add(dir, keepAlive)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cata link add: %v\n", err)
		os.Exit(1)
	}

	// 注册即常驻：拉起 agent；若配了网关则由 agent 自持隧道。
	if err := link.EnsureAgent(entry.AgentID); err != nil {
		fmt.Fprintf(os.Stderr, "cata link add: ensure agent: %v\n", err)
		os.Exit(1)
	}
	// 有常驻 agent 时自动拉起 supervisor 保活。
	if keepAlive {
		if err := link.EnsureSupervisorDaemon(); err != nil {
			fmt.Fprintf(os.Stderr, "cata link add: ensure supervisor: %v\n", err)
		}
	}

	fmt.Printf("linked: agent_id=%s name=%s root=%s keep_alive=%t\n",
		entry.AgentID, entry.Name, entry.RootPath, entry.KeepAlive)
	if link.AgentAlive(entry.AgentID) {
		fmt.Printf("agent running: %s\n", entry.AgentID)
	} else {
		fmt.Printf("warning: agent not running yet; check `cata supervisor` / logs/agent-%s.log\n", entry.AgentID)
	}
}

func runLinkRemove(args []string) {
	if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintln(os.Stderr, "cata link remove: <agent_id> required")
		os.Exit(2)
	}
	agentID := args[0]
	if err := link.Remove(agentID); err != nil {
		fmt.Fprintf(os.Stderr, "cata link remove: %v\n", err)
		os.Exit(1)
	}
	if err := link.StopAgent(agentID); err != nil {
		fmt.Fprintf(os.Stderr, "cata link remove: stop agent: %v (registration removed)\n", err)
	} else {
		fmt.Printf("removed and stopped agent: %s\n", agentID)
	}
}

func runLinkList() {
	entries, err := link.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cata link list: %v\n", err)
		os.Exit(1)
	}
	if len(entries) == 0 {
		fmt.Println("no linked workspaces (use: cata link add --dir <path>)")
		return
	}
	for _, e := range entries {
		state := "off"
		if link.AgentAlive(e.AgentID) {
			state = "running"
		}
		fmt.Printf("%s\t%s\t%s\tkeep_alive=%t enabled=%t %s\n",
			e.AgentID, e.Name, e.RootPath, e.KeepAlive, e.Enabled, state)
	}
}

func runLinkStatus() {
	entries, err := link.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cata link status: %v\n", err)
		os.Exit(1)
	}
	supAlive := link.SupervisorAlive()
	fmt.Printf("supervisor: %s\n", map[bool]string{true: "running", false: "not running"}[supAlive])
	for _, e := range entries {
		fmt.Printf("%s\tkeep_alive=%t enabled=%t running=%t\n",
			e.AgentID, e.KeepAlive, e.Enabled, link.AgentAlive(e.AgentID))
	}
}

// runSupervisor 每机器一个进程生命周期守护（cata supervisor）。
func runSupervisor(args []string) {
	if len(args) == 1 && (args[0] == "stop") {
		if err := link.StopSupervisor(); err != nil {
			fmt.Fprintf(os.Stderr, "cata supervisor stop: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("supervisor shutdown requested: 已停止 supervisor 及全部保活 cata agent")
		return
	}
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Println("Usage: cata supervisor [stop]")
			fmt.Println("  （无参数）运行守护：Per-machine daemon, keep registered (linked) agent processes alive;")
			fmt.Println("  stop        关闭守护并级联停止全部保活的 cata agent")
			fmt.Println("  control socket at ~/.cata/supervisor.sock (ensure/stop/list/status/shutdown).")
			return
		}
		fmt.Fprintf(os.Stderr, "cata supervisor: unknown flag %q\n", a)
		os.Exit(2)
	}
	if err := link.RunSupervisor(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "cata supervisor: %v\n", err)
		os.Exit(1)
	}
}
