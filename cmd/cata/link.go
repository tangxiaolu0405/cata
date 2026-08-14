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
//	cata link add --dir <path> [--gateway <url>] [--token <token>]
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
	fmt.Println("  cata link add --dir <path> [--gateway <url>] [--token <token>]")
	fmt.Println("  cata link remove <agent_id>")
	fmt.Println("  cata link list")
	fmt.Println("  cata link status")
	fmt.Println()
	fmt.Println("Register a local workspace to a remote cata-gateway. agent_id = workspace id.")
	fmt.Println("Registered workspaces run a keep-alive `cata agent` (one LLM loop per workspace)")
	fmt.Println("and, when a gateway is configured, hold a WSS tunnel to the gateway.")
	fmt.Println()
	fmt.Println("Config: ~/.cata/link.json  (gateway_url / token / agents)")
}

func runLinkAdd(args []string) {
	dir := ""
	gatewayURL := ""
	token := ""
	keepAlive := true
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--dir" && i+1 < len(args):
			dir = args[i+1]
			i++
		case a == "--gateway" && i+1 < len(args):
			gatewayURL = args[i+1]
			i++
		case a == "--token" && i+1 < len(args):
			token = args[i+1]
			i++
		case a == "--no-keep-alive":
			keepAlive = false
		case a == "-h" || a == "--help":
			fmt.Println("Usage: cata link add --dir <path> [--gateway <url>] [--token <token>] [--no-keep-alive]")
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

	entry, err := link.Add(dir, keepAlive, gatewayURL, token)
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
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Println("Usage: cata supervisor")
			fmt.Println("  Per-machine daemon: keep registered (linked) agent processes alive;")
			fmt.Println("  control socket at ~/.cata/supervisor.sock (ensure/stop/list/status).")
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
