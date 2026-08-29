package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
)

// Edition gateway 发行档位（由配置决定，非不同二进制）。
//   - base：gateway + 本机 per-ws agent 一体（默认 auto_start，确保 supervisor 保活常驻 agent）
//   - channel：仅渠道适配，agent 进程按需/外部运行（默认）
//   - remote：云端注册中心 + 路由，接受各机器 `cata agent --link` 的 WSS 隧道
const (
	EditionBase    = "base"
	EditionChannel = "channel"
	EditionRemote  = "remote"
)

// CataServerMode worker 连接方式。
const (
	ServerModeSocket   = "socket"   // 本机 per-ws agent socket（模式一）
	ServerModeExternal = "external" // 同 socket，但不自动确保 supervisor（用户自管进程）
	ServerModeRemote   = "remote"   // 云端注册中心 + WSS 隧道（模式三）
)

// CataServerConfig gateway 侧的 cata worker 配置。
// 说明：本地多空间已迁移为 per-ws agent（每项目独立进程，socket 在
// ~/.cata/sockets/<ws_id>.sock），不再拉起 legacy cata run。Binary/Managed/StopOnExit
// 为历史字段，保留仅用于向后兼容，当前运行时不读取。
type CataServerConfig struct {
	// Mode：socket（base 默认）、external（仅连接已有 agent）、remote（云端）
	Mode string `json:"mode,omitempty"`
	// Binary 历史字段（legacy cata run 用），现不读取
	Binary string `json:"binary,omitempty"`
	// AutoStart 启动 gateway 时是否确保 supervisor（保活常驻 agent）
	AutoStart bool `json:"auto_start,omitempty"`
	// Managed 历史字段（legacy cata run --managed 用），现不读取
	Managed bool `json:"managed,omitempty"`
	// StopOnExit 历史字段（legacy server 回收用），现不读取
	StopOnExit bool `json:"stop_on_exit,omitempty"`
}

// Project 本地 UI 管理的真实项目目录（多项目并行主工作场）。
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
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
	CataURL            string           `json:"cata_url,omitempty"`  // 模式二/三预留
	UIListen           string           `json:"ui_listen,omitempty"` // 控制台监听，默认 0.0.0.0:8787；off 关闭
	// UIPassword 控制台访问口令。支持两种形态：
	//   - bcrypt hash（以 $2 开头，推荐）：用 `cata-gateway passwd` 生成后写入；
	//   - 明文（向后兼容，启动时打警告建议换 hash）。
	// 空 = 不启用登录页（仍受 LAN-only 限制）。
	UIPassword string `json:"ui_password,omitempty"`
	// LoginBanMaxAttempts 连续登录失败封禁阈值（默认 5）；仅 ui_password 启用时生效。
	LoginBanMaxAttempts int `json:"login_ban_max_attempts,omitempty"`
	// LoginBanDurationSeconds 封禁时长（秒，默认 600=10 分钟）；仅 ui_password 启用时生效。
	LoginBanDurationSeconds int       `json:"login_ban_duration_seconds,omitempty"`
	Projects                []Project `json:"projects,omitempty"`
	// remote 模式（cata_server.mode=remote）：本网关作为云端注册中心 + 路由。
	// GatewayToken 隧道共享 Bearer token（v1；逐 agent token 留 v2）。空 = 拒绝所有隧道。
	GatewayToken string `json:"gateway_token,omitempty"`
	// TunnelListen 隧道/agents API 监听地址（默认 0.0.0.0:8799）。
	TunnelListen string `json:"tunnel_listen,omitempty"`
	// AllowAgentIDs 允许注册的 agent_id 白名单；空 = 放行所有（仍要求 token）。
	AllowAgentIDs []string `json:"allow_agent_ids,omitempty"`
	// DefaultAgentID 通道类会话（telegram/qq）在远端默认路由到的 agent（空 = 第一个在线）。
	DefaultAgentID string `json:"default_agent_id,omitempty"`
}

// DefaultUIListen 控制台默认监听地址（全网卡，便于局域网手机访问）。
const DefaultUIListen = "0.0.0.0:8787"

// DefaultTunnelListen 隧道端点默认监听地址（remote 模式；全网卡便于各机器 agent 接入）。
const DefaultTunnelListen = "0.0.0.0:8799"

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
	if v, ok := os.LookupEnv("CATA_GATEWAY_UI"); ok {
		cfg.UIListen = strings.TrimSpace(v)
	}
	if v := strings.TrimSpace(os.Getenv("CATA_GATEWAY_UI_PASSWORD")); v != "" {
		cfg.UIPassword = v
	}
	if v := strings.TrimSpace(os.Getenv("CATA_GATEWAY_LOGIN_BAN_MAX")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.LoginBanMaxAttempts = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("CATA_GATEWAY_LOGIN_BAN_SECONDS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.LoginBanDurationSeconds = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("CATA_GATEWAY_TOKEN")); v != "" {
		cfg.GatewayToken = v
	}
	if v := strings.TrimSpace(os.Getenv("CATA_TUNNEL_LISTEN")); v != "" {
		cfg.TunnelListen = v
	}
	if v := strings.TrimSpace(os.Getenv("CATA_GATEWAY_ALLOW_AGENTS")); v != "" {
		cfg.AllowAgentIDs = parseStringList(v)
	}
	if v := strings.TrimSpace(os.Getenv("CATA_GATEWAY_DEFAULT_AGENT")); v != "" {
		cfg.DefaultAgentID = v
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
		switch c.Edition {
		case EditionBase:
			c.CataServer.Mode = ServerModeSocket
		case EditionRemote:
			c.CataServer.Mode = ServerModeRemote
		default:
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

// RemoteMode 本网关是否运行在 remote（云端注册中心+路由）模式。
func (c Config) RemoteMode() bool {
	return c.CataServer.Mode == ServerModeRemote
}

// TunnelEnabled remote 模式（隧道由逐机器 token 鉴权，不再依赖固定 gateway_token）。
func (c Config) TunnelEnabled() bool {
	return c.RemoteMode()
}

// ResolvedTunnelListen 返回隧道端点实际监听地址。
func (c Config) ResolvedTunnelListen() string {
	v := strings.TrimSpace(c.TunnelListen)
	if v == "" {
		return DefaultTunnelListen
	}
	return v
}

// EditionLabel 用于日志。
func (c Config) EditionLabel() string {
	switch c.Edition {
	case EditionBase:
		return "base (gateway + local cata server)"
	case EditionRemote:
		return "remote (cloud registry + tunnel routing)"
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

// ResolvedUIListen 返回实际监听地址；空表示关闭 UI。
func (c Config) ResolvedUIListen() string {
	v := strings.TrimSpace(c.UIListen)
	switch strings.ToLower(v) {
	case "0", "false", "off", "disabled", "no":
		return ""
	case "":
		return DefaultUIListen
	default:
		return v
	}
}

// UIEnabled 是否启动本地控制台。
func (c Config) UIEnabled() bool {
	return c.ResolvedUIListen() != ""
}

// LoginBanMaxAttemptsDefault 默认连续失败封禁阈值。
const LoginBanMaxAttemptsDefault = 5

// LoginBanDurationDefault 默认封禁时长（10 分钟）。
const LoginBanDurationDefault = 10 * time.Minute

// ResolvedLoginBanMaxAttempts 连续失败封禁阈值（默认 LoginBanMaxAttemptsDefault）。
func (c Config) ResolvedLoginBanMaxAttempts() int {
	if c.LoginBanMaxAttempts <= 0 {
		return LoginBanMaxAttemptsDefault
	}
	return c.LoginBanMaxAttempts
}

// ResolvedLoginBanDuration 封禁时长（默认 LoginBanDurationDefault=10 分钟）。
func (c Config) ResolvedLoginBanDuration() time.Duration {
	if c.LoginBanDurationSeconds <= 0 {
		return LoginBanDurationDefault
	}
	return time.Duration(c.LoginBanDurationSeconds) * time.Second
}

// FindProject 按 id 查找项目。
func (c Config) FindProject(id string) (Project, bool) {
	id = strings.TrimSpace(id)
	for _, p := range c.Projects {
		if p.ID == id {
			return p, true
		}
	}
	return Project{}, false
}

// ConfigPath 返回 ~/.cata/gateway.json。
func ConfigPath() string {
	return filepath.Join(brain.CataHome(), "gateway.json")
}

// GatewayKnownTopKeys gateway.json 中由 Config 结构体占用的顶层键。
func GatewayKnownTopKeys() []string {
	return []string{
		"edition", "cata_server",
		"telegram_bot_token", "telegram_allowed_user_ids",
		"qq_app_id", "qq_app_secret", "qq_allowed_openids", "qq_sandbox",
		"worker_root", "socket_path", "cata_url", "ui_listen", "projects",
		"gateway_token", "tunnel_listen", "allow_agent_ids", "default_agent_id",
		"ui_password", "login_ban_max_attempts", "login_ban_duration_seconds",
	}
}

// SaveConfig 合并写回 gateway.json，保留未知顶层键。
func SaveConfig(cfg Config) error {
	if err := brain.EnsureCataLayout(); err != nil {
		return err
	}
	path := ConfigPath()
	doc := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &doc)
	}
	if doc == nil {
		doc = map[string]json.RawMessage{}
	}
	typed, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	var typedMap map[string]json.RawMessage
	if err := json.Unmarshal(typed, &typedMap); err != nil {
		return err
	}
	for _, k := range GatewayKnownTopKeys() {
		delete(doc, k)
	}
	for k, v := range typedMap {
		doc[k] = v
	}
	out, err := json.MarshalIndent(rawMessageDoc(doc), "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(path, out, 0600)
}

// rawMessageDoc 把 RawMessage map 转成可 MarshalIndent 的 any map。
func rawMessageDoc(doc map[string]json.RawMessage) map[string]any {
	out := make(map[string]any, len(doc))
	for k, v := range doc {
		var val any
		if err := json.Unmarshal(v, &val); err != nil {
			out[k] = json.RawMessage(v)
			continue
		}
		out[k] = val
	}
	return out
}

// LoadGatewayDocument 从磁盘读取 gateway.json（不应用环境变量覆盖），并拆出未知顶层键。
func LoadGatewayDocument() (cfg Config, extras map[string]json.RawMessage, err error) {
	extras = map[string]json.RawMessage{}
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			cfg.normalize()
			return cfg, extras, nil
		}
		return Config{}, nil, err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return Config{}, nil, err
	}
	known := map[string]struct{}{}
	for _, k := range GatewayKnownTopKeys() {
		known[k] = struct{}{}
	}
	typed := map[string]json.RawMessage{}
	for k, v := range doc {
		if _, ok := known[k]; ok {
			typed[k] = v
		} else {
			extras[k] = v
		}
	}
	tb, err := json.Marshal(typed)
	if err != nil {
		return Config{}, nil, err
	}
	if err := json.Unmarshal(tb, &cfg); err != nil {
		return Config{}, nil, err
	}
	cfg.normalize()
	return cfg, extras, nil
}

// SaveGatewayDocument 写回已知配置；extras 非 nil 时替换全部未知顶层键。
func SaveGatewayDocument(cfg Config, extras map[string]json.RawMessage) error {
	if err := brain.EnsureCataLayout(); err != nil {
		return err
	}
	path := ConfigPath()
	doc := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &doc)
	}
	if doc == nil {
		doc = map[string]json.RawMessage{}
	}
	known := map[string]struct{}{}
	for _, k := range GatewayKnownTopKeys() {
		known[k] = struct{}{}
	}
	typed, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	var typedMap map[string]json.RawMessage
	if err := json.Unmarshal(typed, &typedMap); err != nil {
		return err
	}
	for _, k := range GatewayKnownTopKeys() {
		delete(doc, k)
	}
	for k, v := range typedMap {
		doc[k] = v
	}
	if extras != nil {
		for k := range doc {
			if _, ok := known[k]; !ok {
				delete(doc, k)
			}
		}
		for k, v := range extras {
			if _, ok := known[k]; ok {
				continue
			}
			doc[k] = v
		}
	}
	out, err := json.MarshalIndent(rawMessageDoc(doc), "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(path, out, 0600)
}

// SaveProjects 仅更新 projects 字段后写回（先读盘再合并，避免丢密钥与未知键）。
func SaveProjects(projects []Project) (Config, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return Config{}, err
	}
	cfg.Projects = projects
	if err := SaveConfig(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
