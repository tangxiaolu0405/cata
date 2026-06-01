package main

import (
	"fmt"
	"os"
	"path/filepath"

	"cata/internal/brain"
	"cata/internal/client"
	"cata/internal/config"
	"cata/internal/server"
)

func main() {
	if err := config.InitBrainPath(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to initialize brain path: %v\n", err)
		os.Exit(1)
	}

	if len(os.Args) < 2 {
		client.RunChat(nil)
		return
	}

	command := os.Args[1]
	switch command {
	case "help", "--help", "-h":
		printUsage()
	case "chat":
		dirs := parseDirs(os.Args[2:])
		client.RunChat(dirs)
	case "init":
		runInit()
	case "config":
		handleConfigCommand(os.Args[2:])
	case "run":
		runServer(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Error: Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func parseDirs(args []string) []string {
	var dirs []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--dir" && i+1 < len(args) {
			abs, err := filepath.Abs(args[i+1])
			if err == nil {
				dirs = append(dirs, abs)
			}
			i++
		}
	}
	return dirs
}

func printUsage() {
	fmt.Println("Cata — terminal agent (one binary: server + chat client)")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  cata                    Start chat (default)")
	fmt.Println("  cata chat [--dir <path>]  Start chat at output dir")
	fmt.Println("  cata run                Start server (one per machine; foreground)")
	fmt.Println("  cata init               Initialize ~/.cata brain layout")
	fmt.Println("  cata config             Manage configuration")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  cata                         # uses current directory as output area")
	fmt.Println("  cata chat --dir ~/project    # project as output area")
	fmt.Println("  cata chat --dir ~/a --dir ~/b  # first dir is main output area")
	fmt.Println()
	fmt.Println("Same output directory: second `cata` exits with an error.")
	fmt.Println("See README.md and agents.md")
}

func runInit() {
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	if err := brain.InitDirectory(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
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
	config.ApplyInitDefaults(cfg)

	configPath := config.GetConfigPath()
	created := false
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		created = true
	}
	if err := config.SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save config file: %v\n", err)
	} else if created {
		fmt.Printf("Configuration file created: %s\n", configPath)
	} else {
		fmt.Printf("Configuration file updated: %s\n", configPath)
	}

	fmt.Printf("Brain directory initialized successfully!\n")
	fmt.Printf("Brain directory: %s\n", cfg.Brain.Dir)
	fmt.Printf("Configuration file: %s\n", configPath)
	fmt.Println("\nConfiguration:")
	fmt.Printf("  LLM Provider: %s\n", cfg.LLM.Provider)
	fmt.Printf("  LLM Enabled: %v\n", cfg.LLM.Enabled)
	fmt.Printf("  Autonomous evolution: enabled=%v cycle_interval=%ds\n",
		cfg.Evolution.Enabled, cfg.Evolution.CycleInterval)
	fmt.Printf("  run_command (exec): enabled=%v\n", cfg.Exec.Enabled)
	fmt.Printf("  timezone: %s\n", cfg.Server.Timezone)
	fmt.Println("\nNext: cata")
}

func runServer(args []string) {
	managed := false
	for _, a := range args {
		if a == "--managed" {
			managed = true
			break
		}
	}

	if config.Config == nil {
		cfg, err := config.LoadConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to load config: %v\n", err)
		} else {
			config.Config = cfg
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
