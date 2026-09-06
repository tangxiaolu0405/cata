package gateway

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"cata/internal/cata/brain"
)

const defaultWorkerRootName = ".cata_work"

var safePathSegment = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// WorkerRoot 返回 gateway 产出区根目录（默认 ~/.cata_work）。
func WorkerRoot(cfgRoot string) string {
	if r := strings.TrimSpace(cfgRoot); r != "" {
		return r
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(brain.CataHome(), defaultWorkerRootName)
	}
	return filepath.Join(home, defaultWorkerRootName)
}

// ParseSessionKey 解析 "channel:id" 格式的会话键。
func ParseSessionKey(key SessionKey) (channel, id string, ok bool) {
	s := string(key)
	i := strings.IndexByte(s, ':')
	if i <= 0 || i >= len(s)-1 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

// WorkerCwd 每个渠道会话的 cata 产出区：{worker_root}/{channel}/{chat_id}/。
func WorkerCwd(workerRoot string, channel, chatID string) (string, error) {
	channel = strings.TrimSpace(channel)
	chatID = strings.TrimSpace(chatID)
	if channel == "" || chatID == "" {
		return "", fmt.Errorf("worker cwd: empty channel or chat id")
	}
	channel = safePathSegment.ReplaceAllString(channel, "_")
	chatID = safePathSegment.ReplaceAllString(chatID, "_")
	if channel == "" || chatID == "" {
		return "", fmt.Errorf("worker cwd: invalid channel or chat id")
	}
	root := WorkerRoot(workerRoot)
	return filepath.Join(root, channel, chatID), nil
}

// WorkerCwdForSession 从 SessionKey 解析产出区路径并确保目录存在。
func WorkerCwdForSession(workerRoot string, key SessionKey) (string, error) {
	channel, id, ok := ParseSessionKey(key)
	if !ok {
		return "", fmt.Errorf("invalid session key: %q", key)
	}
	dir, err := WorkerCwd(workerRoot, channel, id)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

// SessionKeyFor 构造标准会话键。
func SessionKeyFor(channel, chatID string) SessionKey {
	return SessionKey(channel + ":" + chatID)
}
