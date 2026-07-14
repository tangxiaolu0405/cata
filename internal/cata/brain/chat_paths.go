package brain

import (
	"fmt"
	"path/filepath"
	"strings"

	"cata/internal/cata/config"
)

const (
	// ChatBrainPrefix 脑子 workspace 格路径前缀（相对 chat 工具 path）。
	ChatBrainPrefix = "brain/"
	// ChatGlobalPrefix CATA_HOME/global 虚拟路径前缀。
	ChatGlobalPrefix = "global/"
)

// ResolveChatFilePath 解析 chat 文件工具路径：产出区（默认）、brain/*、global/*。
func ResolveChatFilePath(rel string) (abs string, err error) {
	rel = strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/"))
	if rel == "" || rel == "." {
		return chatOutputBase()
	}
	rel = strings.TrimPrefix(rel, "./")

	if rel == "brain" || strings.HasPrefix(rel, ChatBrainPrefix) {
		w := Active()
		if w == nil {
			return "", fmt.Errorf("no active workspace brain")
		}
		sub := strings.TrimPrefix(rel, "brain/")
		sub = strings.TrimPrefix(sub, "./")
		if sub == "" || sub == "." {
			return filepath.Abs(w.ProjectCataRoot())
		}
		if sub == "memory" || strings.HasPrefix(sub, "memory/") {
			if sub == "memory" {
				return filepath.Abs(filepath.Join(w.Dir(), "memory"))
			}
			return PathUnderBase(w.Dir(), filepath.FromSlash(sub))
		}
		if sub == RelMetaJSON || sub == RelEvolutionLog {
			return PathUnderBase(w.Dir(), filepath.FromSlash(sub))
		}
		return PathUnderBase(w.ProjectCataRoot(), filepath.FromSlash(sub))
	}

	if rel == "global" || strings.HasPrefix(rel, ChatGlobalPrefix) {
		sub := strings.TrimPrefix(rel, "global/")
		sub = strings.TrimPrefix(sub, "./")
		if sub == "" || sub == "." {
			return filepath.Abs(globalDir())
		}
		return PathUnderBase(globalDir(), filepath.FromSlash(sub))
	}

	base, err := chatOutputBase()
	if err != nil {
		return "", err
	}
	return PathUnderBase(base, filepath.FromSlash(rel))
}

func chatOutputBase() (string, error) {
	base := OutputCwd()
	if base == "" {
		base = config.GetBrainBaseDir()
	}
	if base == "" {
		return "", fmt.Errorf("output workspace not configured")
	}
	return filepath.Abs(base)
}
