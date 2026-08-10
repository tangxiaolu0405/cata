package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

func normalizeExecConfig(e *ExecToolConfig) {
	if e == nil {
		return
	}
	if e.MaxOutputBytes <= 0 {
		e.MaxOutputBytes = 256 * 1024
	}
	if e.TimeoutSeconds <= 0 {
		e.TimeoutSeconds = 120
	}
}

func execArgv0Base(argv0 string) string {
	s := strings.TrimSpace(argv0)
	s = filepath.Base(s)
	s = strings.ToLower(s)
	if strings.HasSuffix(s, ".exe") {
		s = strings.TrimSuffix(s, ".exe")
	}
	return s
}

var defaultExecWhitelist = []string{
	"git", "go", "npm", "node", "npx", "pnpm", "yarn", "python", "python3", "pip", "pip3",
	"cargo", "rustc", "make", "cmake", "docker", "kubectl", "bash", "sh", "zsh",
	"ls", "cat", "head", "tail", "grep", "rg", "find", "sed", "awk", "chmod", "mkdir",
	"cp", "mv", "touch", "echo", "pwd", "which", "where", "type", "dir", "cmd", "powershell",
	"wsl", "code", "cursor",
}

func execAllowAllWhitelist(w []string) bool {
	for _, x := range w {
		if strings.TrimSpace(x) == "*" {
			return true
		}
	}
	return false
}

func matchesExecWhitelistItem(base, item string) bool {
	item = strings.ToLower(strings.TrimSpace(item))
	if item == "" || item == "*" {
		return false
	}
	if base == item {
		return true
	}
	if strings.Contains(item, "*") || strings.Contains(item, "?") {
		ok, _ := filepath.Match(item, base)
		return ok
	}
	if strings.HasPrefix(base, item+"-") || strings.HasPrefix(base, item+".") {
		return true
	}
	return false
}

// CheckExecArgv 黑白名单校验（整条命令行小写子串匹配 blacklist）。
func CheckExecArgv(argv []string) error {
	if Config == nil {
		return fmt.Errorf("config not loaded")
	}
	if len(argv) == 0 {
		return fmt.Errorf("argv required")
	}
	line := strings.ToLower(strings.Join(argv, " "))
	for _, b := range Config.Exec.Blacklist {
		b = strings.ToLower(strings.TrimSpace(b))
		if b != "" && strings.Contains(line, b) {
			return fmt.Errorf("command blocked by blacklist")
		}
	}
	wl := Config.Exec.Whitelist
	if execAllowAllWhitelist(wl) {
		return nil
	}
	if len(wl) == 0 {
		wl = defaultExecWhitelist
	}
	base := execArgv0Base(argv[0])
	for _, item := range wl {
		if matchesExecWhitelistItem(base, item) {
			return nil
		}
	}
	return fmt.Errorf("argv[0] %q not in exec whitelist", argv[0])
}

// ExecNeedsConfirm require_confirm=true 时每条都确认；否则仅 blacklist 命中时确认。
func ExecNeedsConfirm(argv []string) bool {
	if Config == nil {
		return true
	}
	ec := &Config.Exec
	if ec.RequireConfirm {
		return true
	}
	line := strings.ToLower(strings.Join(argv, " "))
	for _, b := range ec.Blacklist {
		b = strings.ToLower(strings.TrimSpace(b))
		if b != "" && strings.Contains(line, b) {
			return true
		}
	}
	return false
}

// InitBrainPath 加载配置并解析 brain 与基目录路径。
