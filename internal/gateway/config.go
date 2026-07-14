package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
)

// Edition gateway 发行档位（由配置决定，非不同二进制）。
//   - base：gateway + 本机 cata server 一体（默认自动拉起 worker）
//   - channel：仅渠道适配，worker 需外部运行（默认）
const (
	EditionBase    = "base"
	EditionChannel = "channel"
)

// CataServerMode worker 连接方式。
const (
	ServerModeSocket   = "socket"   // 本机 Unix socket（模式一）
	ServerModeExternal = "external" // 同 socket，但不自动启动（用户自管 cata run）
	ServerModeRemote   = "remote"   // 预留：HTTP CATA_URL（模式二/三）
)

// CataServerConfig gateway 侧的 cata worker 配置（base 版核心）。
type CataServerConfig struct {
	// Mode：socket（base 默认，自动启动）、external（仅连接已有 socket）、remote（预留）
	Mode string `json:"mode,omitempty"`
	// Binary cata 可执行文件；空则 CATA_BIN 或 PATH 上的 cata
	Binary string `json:"binary,omitempty"`
	// AutoStart 启动 gateway 时是否确保 cata server 在运行
	AutoStart bool `json:"auto_start,omitempty"`
	// Managed 拉起时是否带 `cata run --managed`（false 则普通 `cata run`）
	Managed bool `json:"managed,omitempty"`
	// StopOnExit gateway 退出时是否结束由本进程拉起的 server
	StopOnExit bool `json:"stop_on_exit,omitempty"`
}

// Config gateway 运行配置（环境变量优先，其次 ~/.cata/gateway.json）。
type Config struct {
	Edition            string           `json:"edition,omitempty"` // base | channel
	CataServer         CataServerConfig `json:"cata_server,omitempty"`
	TelegramBotToken   string           `json:"telegram_bot_token"`
	TelegramAllowedIDs []int64          `json:"telegram_allowed_user_ids,omitempty"`
	QQAppID            string           `json:"qq_app_id,omitempty"`
	QQAppSecret        string           `json:"qq_app_secret,omitempty"`
	QQAllowedOpenIDs   []string         `json:"qq_allowed_openids,omitempty"`
	QQSandbox          bool             `json:"qq_sandbox,omitempty"`
	WorkerRoot         string           `json:"worker_root,omitempty"`
	SocketPath         string           `json:"socket_path,omitempty"`
	CataURL            string           `json:"cata_url,omitempty"` // 模式二/三预留
}

// LoadConfig 读取 gateway 配置。
func LoadConfig() (Config, error) {
	if err := config.InitBrainPath(); err != nil {
		return Config{}, err
	}
	var cfg Config
	if data, err := os.ReadFile(filepath.Join(brain.CataHome(), "gateway.json")); err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	applyEnvOverrides(&cfg)
	cfg.normalize()
	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if v := strings.TrimSpace(os.Getenv("CATA_GATEWAY_EDITION")); v != "" {
		cfg.Edition = v
	}
	if v := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")); v != "" {
		cfg.TelegramBotToken = v
	}
	if v := strings.TrimSpace(os.Getenv("CATA_WORKER_ROOT")); v != "" {
		cfg.WorkerRoot = v
	}
	if v := strings.TrimSpace(os.Getenv("CATA_SOCKET")); v != "" {
		cfg.SocketPath = v
	}
	if v := strings.TrimSpace(os.Getenv("CATA_URL")); v != "" {
		cfg.CataURL = v
	}
	if v := strings.TrimSpace(os.Getenv("TELEGRAM_ALLOWED_USERS")); v != "" {
		cfg.TelegramAllowedIDs = parseIDList(v)
	}
	if v := strings.TrimSpace(os.Getenv("CATA_SERVER_MODE")); v != "" {
		cfg.CataServer.Mode = v
	}
	if v := strings.TrimSpace(os.Getenv("CATA_BIN")); v != "" {
		cfg.CataServer.Binary = v
	}
	if v := strings.TrimSpace(os.Getenv("CATA_SERVER_AUTO_START")); v != "" {
		cfg.CataServer.AutoStart = envBool(v)
	}
	if v := strings.TrimSpace(os.Getenv("QQ_APP_ID")); v != "" {
		cfg.QQAppID = v
	}
	if v := strings.TrimSpace(os.Getenv("QQ_APP_SECRET")); v != "" {
		cfg.QQAppSecret = v
	}
	if v := strings.TrimSpace(os.Getenv("QQ_ALLOWED_OPENIDS")); v != "" {
		cfg.QQAllowedOpenIDs = parseStringList(v)
	}
	if v := strings.TrimSpace(os.Getenv("QQ_SANDBOX")); v != "" {
		cfg.QQSandbox = envBool(v)
	}
}

func envBool(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func (c *Config) normalize() {
	c.Edition = strings.ToLower(strings.TrimSpace(c.Edition))
	if c.Edition == "" {
		c.Edition = EditionChannel
	}
	if strings.TrimSpace(c.CataURL) != "" {
		c.CataServer.Mode = ServerModeRemote
	}
	if c.CataServer.Mode == "" {
		if c.Edition == EditionBase {
			c.CataServer.Mode = ServerModeSocket
		} else {
			c.CataServer.Mode = ServerModeExternal
		}
	}
	if c.Edition == EditionBase {
		if !c.CataServer.AutoStart && os.Getenv("CATA_SERVER_AUTO_START") == "" {
			c.CataServer.AutoStart = true
		}
		if c.CataServer.Mode == ServerModeExternal {
			c.CataServer.Mode = ServerModeSocket
		}
	}
	if c.SocketPath == "" {
		c.SocketPath = config.ResolvedSocketPath()
	}
}

// ShouldAutoStartServer 是否由 gateway 拉起本机 cata server（由 edition 默认值 + cata_server.auto_start 决定）。
func (c Config) ShouldAutoStartServer() bool {
	if c.CataServer.Mode == ServerModeRemote {
		return false
	}
	return c.CataServer.AutoStart
}

// EditionLabel 用于日志。
func (c Config) EditionLabel() string {
	switch c.Edition {
	case EditionBase:
		return "base (gateway + local cata server)"
	default:
		return "channel (gateway only)"
	}
}

func parseIDList(s string) []int64 {
	var out []int64
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err == nil {
			out = append(out, id)
		}
	}
	return out
}

func (c Config) UserAllowed(userID int64) bool {
	if len(c.TelegramAllowedIDs) == 0 {
		return true
	}
	for _, id := range c.TelegramAllowedIDs {
		if id == userID {
			return true
		}
	}
	return false
}

func parseStringList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// TelegramEnabled 是否配置了 Telegram。
func (c Config) TelegramEnabled() bool {
	return strings.TrimSpace(c.TelegramBotToken) != ""
}

// QQEnabled 是否配置了 QQ 开放平台（WebSocket 试验接入）。
func (c Config) QQEnabled() bool {
	return strings.TrimSpace(c.QQAppID) != "" && strings.TrimSpace(c.QQAppSecret) != ""
}

// QQOpenIDAllowed 白名单为空则放行。
func (c Config) QQOpenIDAllowed(openid string) bool {
	if len(c.QQAllowedOpenIDs) == 0 {
		return true
	}
	openid = strings.TrimSpace(openid)
	for _, id := range c.QQAllowedOpenIDs {
		if id == openid {
			return true
		}
	}
	return false
}
