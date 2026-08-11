package client

import (
	"path/filepath"
	"strings"
)

// ChatOptions 客户端启动选项（--dir / --quiet / --verbose / --show-thinking）。
type ChatOptions struct {
	Dirs         []string
	DisplayMode  string // "" | "quiet" | "verbose"
	ShowThinking bool   // 展示模型推理（thinking 事件）
}

// ParseChatOptions extracts chat options from CLI args.
func ParseChatOptions(args []string) ChatOptions {
	var o ChatOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dir":
			if i+1 < len(args) {
				if abs, err := filepath.Abs(args[i+1]); err == nil {
					o.Dirs = append(o.Dirs, abs)
				}
				i++
			}
		case "--quiet", "-q":
			o.DisplayMode = "quiet"
		case "--verbose", "-v":
			o.DisplayMode = "verbose"
		case "--show-thinking":
			o.ShowThinking = true
		}
	}
	return o
}

// ParseOutputDirs extracts absolute paths from `--dir <path>` flags (chat client).
func ParseOutputDirs(args []string) []string {
	return ParseChatOptions(args).Dirs
}

func (o ChatOptions) firstDir() string {
	if len(o.Dirs) > 0 {
		return o.Dirs[0]
	}
	return ""
}

func (o ChatOptions) displayMode() string {
	m := strings.ToLower(o.DisplayMode)
	switch m {
	case "quiet", "verbose":
		return m
	default:
		return ""
	}
}
