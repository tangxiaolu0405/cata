package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cata/internal/cata/clock"
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
	Subagent       SubagentConfig       `json:"subagent"`
	Chat           ChatLoopConfig       `json:"chat"`
	MCP            MCPConfig            `json:"mcp"`
}

// ChatLoopConfig 主 chat 防失控天花板（业务终止条件由 declare_task 按任务声明）。
type ChatLoopConfig struct {
	// HardMaxToolRounds 绝对轮次天花板，防止未声明限额时无限转。默认 200。
	// 任务 max_tool_rounds 若更大也会被裁到此值。
	HardMaxToolRounds int `json:"hard_max_tool_rounds,omitempty"`
}

// ChatHardMaxToolRounds 防失控轮次天花板（默认 200）。不是任务默认终止策略。
func ChatHardMaxToolRounds() int {
	if Config != nil && Config.Chat.HardMaxToolRounds > 0 {
		return Config.Chat.HardMaxToolRounds
	}
	return 200
}

// DefaultPlaywrightMCPVersion 默认 pin 的 @playwright/mcp 版本（避免 @latest 行为漂移）。
const DefaultPlaywrightMCPVersion = "0.0.75"

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
	// Provider 仅作展示标签（deepseek、mimo 等），不参与协议选择。
	Provider string `json:"provider"`
	// APIFormat 协议兜底：openai（Chat Completions 兼容）| anthropic（Messages 兼容）。
	APIFormat     string            `json:"api_format"`
	APIKey        string            `json:"api_key"`
	APIURL        string            `json:"api_url"`
	Model         string            `json:"model"`
	Models        map[string]string `json:"models"`
	MaxTokens     int               `json:"max_tokens"`
	Timeout       int               `json:"timeout"`
	ContextWindow int               `json:"context_window"`
	// Thinking DeepSeek/MiMo 思考模式：auto（有 tools 时 disabled）、enabled、disabled。
	// 仅对支持该扩展的网关生效；OpenAI / Gemini OpenAI 兼容层等不会下发 thinking 字段。
	Thinking string `json:"thinking,omitempty"`
	Enabled  bool   `json:"enabled"`
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
	// DecisionMaxTokens 演进决策 JSON 的 max_tokens；0 或未配置=不限制（请求中省略 max_tokens）。
	DecisionMaxTokens int `json:"decision_max_tokens"`
	// ShortTermTriggerBytes short-term 达到此字节数触发 consolidate 级演进；0=用代码默认（16KiB）。
	ShortTermTriggerBytes int `json:"short_term_trigger_bytes,omitempty"`
	// ShortTermActivityBytes short-term 有活动即可尝试演进的下限；0=用代码默认（512）。
	ShortTermActivityBytes int `json:"short_term_activity_bytes,omitempty"`
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

// SubagentConfig delegate_task worker：并行上限与低成本执行参数。
type SubagentConfig struct {
	MaxConcurrent      int      `json:"max_concurrent,omitempty"`
	DefaultMaxRounds   int      `json:"default_max_rounds,omitempty"`
	MaxToolResultBytes int      `json:"max_tool_result_bytes,omitempty"`
	MaxOutputTokens    int      `json:"max_output_tokens,omitempty"`
	DefaultTools       []string `json:"default_tools,omitempty"`
}

// MaxSubagentConcurrent 同时运行的子 Agent 数（默认 4）。
func MaxSubagentConcurrent() int {
	if Config != nil && Config.Subagent.MaxConcurrent > 0 {
		return Config.Subagent.MaxConcurrent
	}
	return 4
}

// DefaultSubagentMaxRounds delegate_task 默认工具轮次上限（默认 8）。
func DefaultSubagentMaxRounds() int {
	if Config != nil && Config.Subagent.DefaultMaxRounds > 0 {
		return Config.Subagent.DefaultMaxRounds
	}
	return 8
}

// SubagentMaxOutputTokens worker 单次 completion 上限（0 = 用 LLM 客户端默认）。
func SubagentMaxOutputTokens() int {
	if Config != nil && Config.Subagent.MaxOutputTokens > 0 {
		return Config.Subagent.MaxOutputTokens
	}
	return 0
}

// DefaultSubagentTools 未在 delegate_task 指定 tools 时的默认白名单；nil/空=不限制（全套 worker 工具）。
func DefaultSubagentTools() []string {
	if Config == nil || len(Config.Subagent.DefaultTools) == 0 {
		return nil
	}
	return Config.Subagent.DefaultTools
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

// SaveConfig 保存配置文件（见 save_merge.go：合并写回，保留未知顶层键）。

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
			Provider:  getDefaultProviderLabel(),
			APIFormat: NormalizeAPIFormat("", getDefaultAPIURL(), getDefaultProviderLabel()),
			APIKey:    getDefaultAPIKey(),
			APIURL:    getDefaultAPIURL(),
			Model:     getDefaultModel(),
			MaxTokens: 8192,
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
		Subagent: SubagentConfig{
			MaxConcurrent:      4,
			DefaultMaxRounds:   8,
			MaxToolResultBytes: 8192,
		},
		Chat: ChatLoopConfig{
			HardMaxToolRounds: 200,
		},
		MCP: MCPConfig{
			Enabled: true,
			Servers: []MCPServerEntry{{
				Name:    "browser",
				Enabled: true,
				Command: "npx",
				Args:    defaultPlaywrightMCPArgs(),
			}},
			ToolTimeoutSeconds: 300,
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
	if apiKey := os.Getenv("LLM_API_KEY"); apiKey != "" && config.LLM.APIKey == "" {
		config.LLM.APIKey = apiKey
		config.LLM.Enabled = true
	}
	if apiKey := os.Getenv("DEEPSEEK_API_KEY"); apiKey != "" && config.LLM.APIKey == "" {
		config.LLM.APIKey = apiKey
		if config.LLM.Provider == "" {
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
	if config.LLM.APIFormat == "" {
		if f := strings.TrimSpace(os.Getenv("LLM_API_FORMAT")); f != "" {
			config.LLM.APIFormat = f
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
		config.LLM.Provider = getDefaultProviderLabel()
	}
	if config.LLM.APIKey == "" {
		config.LLM.APIKey = getDefaultAPIKey()
	}
	normalizeLLMConfig(&config.LLM)
	if config.LLM.APIURL == "" {
		config.LLM.APIURL = getDefaultAPIURLForFormat(config.LLM.APIFormat)
	}
	if config.LLM.Model == "" {
		config.LLM.Model = getDefaultModelForFormat(config.LLM.APIFormat)
	}
	ensureLLMModelRoles(&config.LLM)
	if config.LLM.APIKey != "" {
		config.LLM.Enabled = true
	}

	normalizeExecConfig(&config.Exec)
	normalizeWorkspaceFiles(&config.WorkspaceFiles)
	normalizeSubagentConfig(&config.Subagent)
	normalizeChatLoopConfig(&config.Chat)
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

// ensureServerTimezone 在 validateAndSetDefaults / init 路径写入默认时区。
func ensureServerTimezone(cfg *AppConfig) {
	if strings.TrimSpace(cfg.Server.Timezone) == "" {
		cfg.Server.Timezone = clock.DefaultTimezone
	}
	_ = clock.Init(cfg.Server.Timezone)
}

func defaultPlaywrightMCPArgs() []string {
	return []string{"-y", "@playwright/mcp@" + DefaultPlaywrightMCPVersion}
}

// migrateLegacyPlaywrightMCPArgs 升级旧默认（@latest + --console）为 pin 版本、去掉 console 噪音。
func migrateLegacyPlaywrightMCPArgs(args []string) ([]string, bool) {
	if !isLegacyPlaywrightMCPArgs(args) {
		return args, false
	}
	return defaultPlaywrightMCPArgs(), true
}

func isLegacyPlaywrightMCPArgs(args []string) bool {
	if len(args) < 2 || len(args) > 3 || args[0] != "-y" || args[1] != "@playwright/mcp@latest" {
		return false
	}
	if len(args) == 2 {
		return true
	}
	return args[2] == "--console"
}

func normalizeMCPConfig(m *MCPConfig) {
	if m == nil {
		return
	}
	if m.ToolTimeoutSeconds <= 0 {
		m.ToolTimeoutSeconds = 300
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
			Args:    defaultPlaywrightMCPArgs(),
		}}
	}
	for i := range m.Servers {
		if m.Servers[i].Name != "browser" {
			continue
		}
		if migrated, ok := migrateLegacyPlaywrightMCPArgs(m.Servers[i].Args); ok {
			m.Servers[i].Args = migrated
		}
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

func normalizeSubagentConfig(s *SubagentConfig) {
	if s == nil {
		return
	}
	if s.MaxConcurrent <= 0 {
		s.MaxConcurrent = 4
	}
	if s.DefaultMaxRounds <= 0 {
		s.DefaultMaxRounds = 8
	}
	if s.MaxToolResultBytes <= 0 {
		s.MaxToolResultBytes = 8192
	}
}

func normalizeChatLoopConfig(c *ChatLoopConfig) {
	if c == nil {
		return
	}
	if c.HardMaxToolRounds <= 0 {
		// 兼容旧配置字段（若仍存在于磁盘 JSON 但结构已删字段，Unmarshal 会忽略）
		c.HardMaxToolRounds = 200
	}
}

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

// getDefaultProviderLabel 返回展示用 provider 标签（不参与协议选择）。
func getDefaultProviderLabel() string {
	if provider := os.Getenv("LLM_PROVIDER"); provider != "" {
		return provider
	}
	if os.Getenv("LLM_API_KEY") != "" {
		return "custom"
	}
	if os.Getenv("DEEPSEEK_API_KEY") != "" {
		return "deepseek"
	}
	if os.Getenv("DASHSCOPE_API_KEY") != "" {
		return "qwen"
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return "anthropic"
	}
	if os.Getenv("OPENAI_API_KEY") != "" {
		return "openai"
	}
	return "openai"
}

func getDefaultAPIKey() string {
	if key := os.Getenv("LLM_API_KEY"); key != "" {
		return key
	}
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
	return getDefaultAPIURLForFormat(NormalizeAPIFormat("", "", getDefaultProviderLabel()))
}

func getDefaultAPIURLForFormat(apiFormat string) string {
	if url := os.Getenv("LLM_API_URL"); url != "" {
		return url
	}
	switch NormalizeAPIFormat(apiFormat, "", "") {
	case "anthropic":
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
	return getDefaultModelForFormat(NormalizeAPIFormat("", "", getDefaultProviderLabel()))
}

// NormalizeAPIFormat 解析 llm.api_format；未配置时默认 openai（provider 标签 claude/anthropic 可推断）。
func NormalizeAPIFormat(apiFormat, _, providerLabel string) string {
	switch strings.ToLower(strings.TrimSpace(apiFormat)) {
	case "openai", "anthropic":
		return strings.ToLower(strings.TrimSpace(apiFormat))
	}
	p := strings.ToLower(strings.TrimSpace(providerLabel))
	if p == "claude" || p == "anthropic" {
		return "anthropic"
	}
	return "openai"
}

// normalizeLLMConfig 规范化 api_format；api_url 保持用户原样（仅 trim），路径由 LLM 客户端运行时探测。
func normalizeLLMConfig(llm *LLMConfig) {
	if llm == nil {
		return
	}
	llm.APIFormat = NormalizeAPIFormat(llm.APIFormat, llm.APIURL, llm.Provider)
	if u := strings.TrimSpace(llm.APIURL); u != "" {
		llm.APIURL = strings.TrimRight(u, "/")
	}
}

// ensureLLMModelRoles 保证 Models map 存在；不强制拆分模型（单模型时各 role 回退 llm.model）。
func ensureLLMModelRoles(llm *LLMConfig) {
	if llm == nil {
		return
	}
	if llm.Models == nil {
		llm.Models = make(map[string]string)
	}
}

func getDefaultModelForFormat(apiFormat string) string {
	if model := os.Getenv("LLM_MODEL"); model != "" {
		return model
	}
	switch NormalizeAPIFormat(apiFormat, "", "") {
	case "anthropic":
		return "claude-sonnet-4-20250514"
	default:
		if model := os.Getenv("OPENAI_MODEL"); model != "" {
			return model
		}
		return "gpt-3.5-turbo"
	}
}
