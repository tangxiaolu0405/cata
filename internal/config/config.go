package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cata/internal/clock"
)

const (
	DefaultBrainDirName  = "brain"
	DefaultAppConfigName = "config.json"
	EnvCataHome          = "CATA_HOME"
	EnvBrainDir          = "CATA_BRAIN_DIR"
	EnvConfigFile        = "CATA_CONFIG_FILE"
	EnvExecEnabled       = "CATA_EXEC_ENABLED"
)

var (
	BrainDir     string
	BrainBaseDir string
	Config       *AppConfig
)

// AppConfig 应用配置（主文件：CATA_HOME/config.json）。
type AppConfig struct {
	Brain          BrainConfig          `json:"brain"`
	LLM            LLMConfig            `json:"llm"`
	Server         ServerConfig         `json:"server"`
	Evolution      EvolutionConfig      `json:"evolution"`
	Exec           ExecToolConfig       `json:"exec"`
	WorkspaceFiles WorkspaceFilesConfig `json:"workspace_files"`
	MCP            MCPConfig            `json:"mcp"`
}

// MCPConfig MCP 工具服务（stdio）；默认 browser 使用 @playwright/mcp。
type MCPConfig struct {
	Enabled            bool             `json:"enabled"`
	Servers            []MCPServerEntry `json:"servers"`
	ToolTimeoutSeconds int              `json:"tool_timeout_seconds"`
	MaxOutputBytes     int              `json:"max_output_bytes"`
	AllowedTools       []string         `json:"allowed_tools"`
	MaxExportedTools   int              `json:"max_exported_tools"`
}

// MCPServerEntry 单个 MCP server 进程配置。
type MCPServerEntry struct {
	Name    string            `json:"name"`
	Enabled bool              `json:"enabled"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

// BrainConfig：Dir=脑子树（默认 CATA_HOME/brain）；BaseDir=产出区/工作区根（项目根）。
type BrainConfig struct {
	Dir     string `json:"dir"`
	BaseDir string `json:"base_dir"`
}

// LLMConfig LLM API 配置。
type LLMConfig struct {
	Provider      string            `json:"provider"`
	APIKey        string            `json:"api_key"`
	APIURL        string            `json:"api_url"`
	Model         string            `json:"model"`
	Models        map[string]string `json:"models"`
	MaxTokens     int               `json:"max_tokens"`
	Timeout       int               `json:"timeout"`
	ContextWindow int               `json:"context_window"`
	// Thinking DeepSeek 思考模式：auto（有 tools 时 disabled）、enabled、disabled
	Thinking  string `json:"thinking,omitempty"`
	Enabled   bool   `json:"enabled"`
}

// ServerConfig 服务器配置。
type ServerConfig struct {
	SocketPath string `json:"socket_path"`
	LogLevel   string `json:"log_level"`
	// Timezone IANA 时区名，如 Asia/Shanghai；影响日志、llm.log、演进与 short-term 时间戳。
	Timezone string `json:"timezone"`
}

// EvolutionConfig 后台自主演进与会话压缩触发。
type EvolutionConfig struct {
	Enabled              bool    `json:"enabled"`
	CycleInterval        int     `json:"cycle_interval"`
	ContextCompressRatio float64 `json:"context_compress_ratio"`
	SessionCompressTurns int     `json:"session_compress_turns"`
}

// ExecToolConfig 终端 chat 的 run_command（os/exec，不经 shell）。
type ExecToolConfig struct {
	Enabled        bool     `json:"enabled"`
	RequireConfirm bool     `json:"require_confirm"`
	Whitelist      []string `json:"whitelist"`
	Blacklist      []string `json:"blacklist"`
	MaxOutputBytes int      `json:"max_output_bytes"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	WorkingDir     string   `json:"working_dir"`
}

// WorkspaceFilesConfig 产出区内 read/search_replace/append（不经 shell）。
type WorkspaceFilesConfig struct {
	Enabled       *bool `json:"enabled,omitempty"`
	MaxReadBytes  int   `json:"max_read_bytes,omitempty"`
	MaxWriteBytes int   `json:"max_write_bytes,omitempty"`
}

// WorkspaceFilesEnabled 文件工具是否启用（缺省 true）。
func (c *AppConfig) WorkspaceFilesEnabled() bool {
	if c == nil {
		return true
	}
	if c.WorkspaceFiles.Enabled == nil {
		return true
	}
	return *c.WorkspaceFiles.Enabled
}

// LoadConfig 加载配置文件。
func LoadConfig() (*AppConfig, error) {
	configPath := getConfigPath()
	if _, err := os.Stat(configPath); err == nil {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		var cfg AppConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}
		applyEnvOverrides(&cfg)
		if err := validateAndSetDefaults(&cfg); err != nil {
			return nil, err
		}
		Config = &cfg
		return &cfg, nil
	}
	cfg := getDefaultConfig()
	applyEnvOverrides(cfg)
	if err := validateAndSetDefaults(cfg); err != nil {
		return nil, err
	}
	Config = cfg
	return cfg, nil
}

// SaveConfig 保存配置文件。
func SaveConfig(config *AppConfig) error {
	configPath := getConfigPath()
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	return os.WriteFile(configPath, data, 0644)
}

// CataHome 状态根：$CATA_HOME 或 ~/.cata。
func CataHome() string {
	if p := strings.TrimSpace(os.Getenv(EnvCataHome)); p != "" {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		wd, _ := os.Getwd()
		return filepath.Join(wd, ".cata")
	}
	return filepath.Join(home, ".cata")
}

func getConfigPath() string {
	if envPath := os.Getenv(EnvConfigFile); envPath != "" {
		return envPath
	}
	return filepath.Join(CataHome(), DefaultAppConfigName)
}

// GetConfigPath 返回主配置文件路径。
func GetConfigPath() string {
	return getConfigPath()
}

func getDefaultConfig() *AppConfig {
	wfOn := true
	return &AppConfig{
		Brain: BrainConfig{},
		LLM: LLMConfig{
			Provider:  getDefaultProvider(),
			APIKey:    getDefaultAPIKey(),
			APIURL:    getDefaultAPIURL(),
			Model:     getDefaultModel(),
			MaxTokens: 2000,
			Timeout:   600,
			Enabled:   getDefaultAPIKey() != "",
		},
		Server: ServerConfig{
			SocketPath: "",
			LogLevel:   "info",
			Timezone:   clock.DefaultTimezone,
		},
		Evolution: EvolutionConfig{
			Enabled:              true,
			CycleInterval:        3600,
			ContextCompressRatio: 0.85,
			SessionCompressTurns: 12,
		},
		Exec: ExecToolConfig{
			Enabled:        true,
			RequireConfirm: false,
			Whitelist:      []string{"*"},
			Blacklist: []string{
				"rm -rf /",
				"mkfs",
				"dd if=/dev/",
				">/dev/sd",
				"| sh",
				"|bash",
				"powershell -e",
			},
			MaxOutputBytes: 256 * 1024,
			TimeoutSeconds: 120,
		},
		WorkspaceFiles: WorkspaceFilesConfig{
			Enabled:       &wfOn,
			MaxReadBytes:  512 * 1024,
			MaxWriteBytes: 512 * 1024,
		},
		MCP: MCPConfig{
			Enabled: true,
			Servers: []MCPServerEntry{{
				Name:    "browser",
				Enabled: true,
				Command: "npx",
				Args:    []string{"-y", "@playwright/mcp@latest", "--console"},
			}},
			ToolTimeoutSeconds: 120,
			MaxOutputBytes:     256 * 1024,
		},
	}
}

func applyEnvOverrides(config *AppConfig) {
	if envDir := os.Getenv(EnvBrainDir); envDir != "" {
		config.Brain.Dir = envDir
	}
	if v := strings.TrimSpace(os.Getenv(EnvExecEnabled)); v == "1" || strings.EqualFold(v, "true") {
		config.Exec.Enabled = true
	}
	if apiKey := os.Getenv("DEEPSEEK_API_KEY"); apiKey != "" && config.LLM.APIKey == "" {
		config.LLM.APIKey = apiKey
		if config.LLM.Provider == "" || config.LLM.Provider == "qwen" {
			config.LLM.Provider = "deepseek"
		}
		config.LLM.Enabled = true
	}
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" && config.LLM.APIKey == "" {
		config.LLM.APIKey = apiKey
		if config.LLM.Provider == "" {
			config.LLM.Provider = "openai"
		}
		config.LLM.Enabled = true
	}
	if apiKey := os.Getenv("DASHSCOPE_API_KEY"); apiKey != "" && config.LLM.APIKey == "" {
		config.LLM.APIKey = apiKey
		if config.LLM.Provider == "" {
			config.LLM.Provider = "qwen"
		}
		config.LLM.Enabled = true
	}
	if apiKey := os.Getenv("ANTHROPIC_API_KEY"); apiKey != "" && config.LLM.APIKey == "" {
		config.LLM.APIKey = apiKey
		if config.LLM.Provider == "" {
			config.LLM.Provider = "claude"
		}
		config.LLM.Enabled = true
	}
	if config.LLM.APIURL == "" {
		if apiURL := os.Getenv("LLM_API_URL"); apiURL != "" {
			config.LLM.APIURL = apiURL
		} else if apiURL := os.Getenv("OPENAI_API_URL"); apiURL != "" {
			config.LLM.APIURL = apiURL
		}
	}
	if config.LLM.Model == "" {
		if model := os.Getenv("LLM_MODEL"); model != "" {
			config.LLM.Model = model
		} else if model := os.Getenv("OPENAI_MODEL"); model != "" {
			config.LLM.Model = model
		}
	}
	if config.LLM.Provider == "" {
		if provider := os.Getenv("LLM_PROVIDER"); provider != "" {
			config.LLM.Provider = provider
		}
	}
}

func validateAndSetDefaults(config *AppConfig) error {
	_ = os.MkdirAll(CataHome(), 0755)

	if strings.TrimSpace(config.Brain.Dir) == "" {
		if envDir := os.Getenv(EnvBrainDir); envDir != "" {
			absPath, err := filepath.Abs(envDir)
			if err != nil {
				return fmt.Errorf("invalid CATA_BRAIN_DIR path: %w", err)
			}
			config.Brain.Dir = absPath
		} else {
			config.Brain.Dir = filepath.Join(CataHome(), DefaultBrainDirName)
		}
	} else {
		absPath, err := filepath.Abs(config.Brain.Dir)
		if err != nil {
			return fmt.Errorf("invalid brain dir path: %w", err)
		}
		config.Brain.Dir = absPath
	}

	if strings.TrimSpace(config.Brain.BaseDir) == "" {
		if root := FindProjectRoot(); root != "" {
			config.Brain.BaseDir = root
		} else {
			wd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get working directory: %w", err)
			}
			config.Brain.BaseDir = wd
		}
	} else {
		absBaseDir, err := filepath.Abs(config.Brain.BaseDir)
		if err != nil {
			return fmt.Errorf("invalid brain base dir path: %w", err)
		}
		config.Brain.BaseDir = absBaseDir
	}

	BrainDir = config.Brain.Dir
	BrainBaseDir = config.Brain.BaseDir

	if config.LLM.Provider == "" {
		config.LLM.Provider = getDefaultProvider()
	}
	if config.LLM.APIKey == "" {
		config.LLM.APIKey = getDefaultAPIKey()
	}
	if config.LLM.APIURL == "" {
		config.LLM.APIURL = getDefaultAPIURLForProvider(config.LLM.Provider)
	}
	if config.LLM.Model == "" {
		config.LLM.Model = getDefaultModelForProvider(config.LLM.Provider)
	}
	normalizeLLMConfig(&config.LLM)
	if config.LLM.APIKey != "" {
		config.LLM.Enabled = true
	}

	normalizeExecConfig(&config.Exec)
	normalizeWorkspaceFiles(&config.WorkspaceFiles)
	normalizeMCPConfig(&config.MCP)

	if config.LLM.Enabled && !config.Exec.Enabled {
		if v := strings.TrimSpace(os.Getenv(EnvExecEnabled)); v == "0" || strings.EqualFold(v, "false") {
			// 显式关闭
		} else {
			config.Exec.Enabled = true
		}
	}

	if config.Evolution.CycleInterval <= 0 {
		config.Evolution.CycleInterval = 3600
	}
	if config.Evolution.ContextCompressRatio <= 0 || config.Evolution.ContextCompressRatio > 1 {
		config.Evolution.ContextCompressRatio = 0.85
	}
	if config.Evolution.SessionCompressTurns <= 0 {
		config.Evolution.SessionCompressTurns = 12
	}

	ensureServerTimezone(config)

	return nil
}

// ApplyInitDefaults 在 cata init 时写入 config.json 的推荐默认值（不覆盖用户已配置的时区等）。
func ApplyInitDefaults(cfg *AppConfig) {
	if cfg == nil {
		return
	}
	ensureServerTimezone(cfg)
}

func ensureServerTimezone(cfg *AppConfig) {
	if strings.TrimSpace(cfg.Server.Timezone) == "" {
		cfg.Server.Timezone = clock.DefaultTimezone
	}
	_ = clock.Init(cfg.Server.Timezone)
}

func normalizeMCPConfig(m *MCPConfig) {
	if m == nil {
		return
	}
	if m.ToolTimeoutSeconds <= 0 {
		m.ToolTimeoutSeconds = 120
	}
	if m.MaxOutputBytes <= 0 {
		m.MaxOutputBytes = 256 * 1024
	}
	if m.MaxExportedTools <= 0 {
		m.MaxExportedTools = 14
	}
	if len(m.AllowedTools) == 0 && m.Enabled {
		m.AllowedTools = []string{"browser_*"}
	}
	if len(m.Servers) == 0 && m.Enabled {
		m.Servers = []MCPServerEntry{{
			Name:    "browser",
			Enabled: true,
			Command: "npx",
			Args:    []string{"-y", "@playwright/mcp@latest", "--console"},
		}}
	}
}

func normalizeWorkspaceFiles(w *WorkspaceFilesConfig) {
	if w == nil {
		return
	}
	if w.MaxReadBytes <= 0 {
		w.MaxReadBytes = 512 * 1024
	}
	if w.MaxWriteBytes <= 0 {
		w.MaxWriteBytes = 512 * 1024
	}
}

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
func InitBrainPath() error {
	if Config == nil {
		var err error
		Config, err = LoadConfig()
		if err != nil {
			return err
		}
	}
	return nil
}

// GetBrainDir 脑子目录（CATA_HOME/brain 或覆盖）。
func GetBrainDir() string {
	if Config == nil {
		InitBrainPath()
	}
	if BrainDir == "" {
		BrainDir = filepath.Join(CataHome(), DefaultBrainDirName)
	}
	return BrainDir
}

// GetBrainBaseDir 产出区/工作区根（brain.base_dir）。
func GetBrainBaseDir() string {
	if Config == nil {
		InitBrainPath()
	}
	if BrainBaseDir == "" {
		if r := FindProjectRoot(); r != "" {
			BrainBaseDir = r
		} else {
			wd, _ := os.Getwd()
			BrainBaseDir = wd
		}
	}
	return BrainBaseDir
}

// GetBrainPath 脑子目录下的相对路径。
func GetBrainPath(relPath string) string {
	return filepath.Join(GetBrainDir(), relPath)
}

// ResolvedSocketPath Unix socket 绝对路径。
func ResolvedSocketPath() string {
	if err := InitBrainPath(); err != nil {
		return filepath.Join(CataHome(), "cata.sock")
	}
	if Config != nil {
		p := strings.TrimSpace(Config.Server.SocketPath)
		if p != "" {
			if filepath.IsAbs(p) {
				return p
			}
			return filepath.Join(GetBrainBaseDir(), p)
		}
	}
	return filepath.Join(CataHome(), "cata.sock")
}

// FindProjectRoot 自 cwd 向上查找含 go.mod 或 .git 的目录。
func FindProjectRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func getDefaultProvider() string {
	if provider := os.Getenv("LLM_PROVIDER"); provider != "" {
		return provider
	}
	if os.Getenv("DEEPSEEK_API_KEY") != "" {
		return "deepseek"
	}
	if os.Getenv("DASHSCOPE_API_KEY") != "" {
		return "qwen"
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return "claude"
	}
	return "openai"
}

func getDefaultAPIKey() string {
	if key := os.Getenv("DEEPSEEK_API_KEY"); key != "" {
		return key
	}
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		return key
	}
	if key := os.Getenv("DASHSCOPE_API_KEY"); key != "" {
		return key
	}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		return key
	}
	return ""
}

func getDefaultAPIURL() string {
	if url := os.Getenv("LLM_API_URL"); url != "" {
		return url
	}
	if url := os.Getenv("OPENAI_API_URL"); url != "" {
		return url
	}
	return getDefaultAPIURLForProvider(getDefaultProvider())
}

func getDefaultAPIURLForProvider(provider string) string {
	if url := os.Getenv("LLM_API_URL"); url != "" {
		return url
	}
	switch provider {
	case "deepseek":
		return "https://api.deepseek.com/chat/completions"
	case "qwen", "tongyi", "dashscope":
		return "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"
	case "claude", "anthropic":
		return "https://api.anthropic.com/v1/messages"
	default:
		if url := os.Getenv("OPENAI_API_URL"); url != "" {
			return url
		}
		return "https://api.openai.com/v1/chat/completions"
	}
}

func getDefaultModel() string {
	if model := os.Getenv("LLM_MODEL"); model != "" {
		return model
	}
	if model := os.Getenv("OPENAI_MODEL"); model != "" {
		return model
	}
	return getDefaultModelForProvider(getDefaultProvider())
}

// normalizeLLMConfig 修正 OpenAI 兼容端点（DeepSeek 等仅需 base_url 时补全路径）。
func normalizeLLMConfig(llm *LLMConfig) {
	if llm == nil {
		return
	}
	u := strings.TrimRight(strings.TrimSpace(llm.APIURL), "/")
	switch strings.ToLower(strings.TrimSpace(llm.Provider)) {
	case "deepseek":
		if u == "https://api.deepseek.com" {
			llm.APIURL = u + "/chat/completions"
		}
	}
}

func getDefaultModelForProvider(provider string) string {
	if model := os.Getenv("LLM_MODEL"); model != "" {
		return model
	}
	switch provider {
	case "deepseek":
		return "deepseek-v4-flash"
	case "qwen", "tongyi", "dashscope":
		return "qwen-turbo"
	case "claude", "anthropic":
		return "claude-3-sonnet-20240229"
	default:
		if model := os.Getenv("OPENAI_MODEL"); model != "" {
			return model
		}
		return "gpt-3.5-turbo"
	}
}
