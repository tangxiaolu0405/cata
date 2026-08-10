package config

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// GetKey returns a config value for dotted keys (e.g. mcp.tool_timeout_seconds).
func GetKey(cfg *AppConfig, key string) (value interface{}, ok bool, err error) {
	if cfg == nil {
		return nil, false, fmt.Errorf("nil config")
	}
	f, ok := configFields[key]
	if !ok {
		return nil, false, nil
	}
	v, err := f.get(cfg)
	return v, true, err
}

// SetKey updates a config value. Returns true when server timezone was changed (caller may clock.Init).
func SetKey(cfg *AppConfig, key, value string) (clockInit bool, err error) {
	if cfg == nil {
		return false, fmt.Errorf("nil config")
	}
	f, ok := configFields[key]
	if !ok {
		return false, fmt.Errorf("unknown config key: %s", key)
	}
	if err := f.set(cfg, value); err != nil {
		return false, err
	}
	return key == "server.timezone", nil
}

// ConfigKeys returns sorted dotted keys supported by get/set.
func ConfigKeys() []string {
	keys := make([]string, 0, len(configFields))
	for k := range configFields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// RedactConfig returns a copy safe for display (masks secrets).
func RedactConfig(cfg *AppConfig) AppConfig {
	if cfg == nil {
		return AppConfig{}
	}
	out := *cfg
	if out.LLM.APIKey != "" {
		out.LLM.APIKey = SecretRedacted
	}
	return out
}

type fieldSpec struct {
	get func(*AppConfig) (interface{}, error)
	set func(*AppConfig, string) error
}

var configFields = map[string]fieldSpec{
	"brain.dir":                        {get: strGet(func(c *AppConfig) string { return c.Brain.Dir }), set: strSet(func(c *AppConfig, v string) { c.Brain.Dir = v })},
	"brain.base_dir":                   {get: strGet(func(c *AppConfig) string { return c.Brain.BaseDir }), set: strSet(func(c *AppConfig, v string) { c.Brain.BaseDir = v })},
	"llm.provider":                     {get: strGet(func(c *AppConfig) string { return c.LLM.Provider }), set: strSet(func(c *AppConfig, v string) { c.LLM.Provider = v })},
	"llm.api_format":                   {get: strGet(func(c *AppConfig) string { return c.LLM.APIFormat }), set: strSet(func(c *AppConfig, v string) { c.LLM.APIFormat = v })},
	"llm.api_key":                      {get: apiKeyGet, set: apiKeySet},
	"llm.api_url":                      {get: strGet(func(c *AppConfig) string { return c.LLM.APIURL }), set: strSet(func(c *AppConfig, v string) { c.LLM.APIURL = v })},
	"llm.model":                        {get: strGet(func(c *AppConfig) string { return c.LLM.Model }), set: strSet(func(c *AppConfig, v string) { c.LLM.Model = v })},
	"llm.max_tokens":                   {get: intGet(func(c *AppConfig) int { return c.LLM.MaxTokens }), set: intSet(func(c *AppConfig, v int) { c.LLM.MaxTokens = v })},
	"llm.timeout":                      {get: intGet(func(c *AppConfig) int { return c.LLM.Timeout }), set: intSet(func(c *AppConfig, v int) { c.LLM.Timeout = v })},
	"llm.context_window":               {get: intGet(func(c *AppConfig) int { return c.LLM.ContextWindow }), set: intSet(func(c *AppConfig, v int) { c.LLM.ContextWindow = v })},
	"llm.thinking":                     {get: strGet(func(c *AppConfig) string { return c.LLM.Thinking }), set: strSet(func(c *AppConfig, v string) { c.LLM.Thinking = v })},
	"llm.enabled":                      {get: boolGet(func(c *AppConfig) bool { return c.LLM.Enabled }), set: boolSet(func(c *AppConfig, v bool) { c.LLM.Enabled = v })},
	"server.socket_path":               {get: strGet(func(c *AppConfig) string { return c.Server.SocketPath }), set: strSet(func(c *AppConfig, v string) { c.Server.SocketPath = v })},
	"server.log_level":                 {get: strGet(func(c *AppConfig) string { return c.Server.LogLevel }), set: strSet(func(c *AppConfig, v string) { c.Server.LogLevel = v })},
	"server.timezone":                  {get: strGet(func(c *AppConfig) string { return c.Server.Timezone }), set: strSet(func(c *AppConfig, v string) { c.Server.Timezone = v })},
	"evolution.enabled":                {get: boolGet(func(c *AppConfig) bool { return c.Evolution.Enabled }), set: boolSet(func(c *AppConfig, v bool) { c.Evolution.Enabled = v })},
	"evolution.cycle_interval":         {get: intGet(func(c *AppConfig) int { return c.Evolution.CycleInterval }), set: intSet(func(c *AppConfig, v int) { c.Evolution.CycleInterval = v })},
	"evolution.context_compress_ratio": {get: floatGet(func(c *AppConfig) float64 { return c.Evolution.ContextCompressRatio }), set: floatSet(func(c *AppConfig, v float64) { c.Evolution.ContextCompressRatio = v })},
	"evolution.session_compress_turns": {get: intGet(func(c *AppConfig) int { return c.Evolution.SessionCompressTurns }), set: intSet(func(c *AppConfig, v int) { c.Evolution.SessionCompressTurns = v })},
	"evolution.decision_max_tokens":    {get: intGet(func(c *AppConfig) int { return c.Evolution.DecisionMaxTokens }), set: intSet(func(c *AppConfig, v int) { c.Evolution.DecisionMaxTokens = v })},
	"evolution.short_term_trigger_bytes": {get: intGet(func(c *AppConfig) int { return c.Evolution.ShortTermTriggerBytes }), set: intSet(func(c *AppConfig, v int) {
		c.Evolution.ShortTermTriggerBytes = v
	})},
	"evolution.short_term_activity_bytes": {get: intGet(func(c *AppConfig) int { return c.Evolution.ShortTermActivityBytes }), set: intSet(func(c *AppConfig, v int) {
		c.Evolution.ShortTermActivityBytes = v
	})},
	"exec.enabled":                    {get: boolGet(func(c *AppConfig) bool { return c.Exec.Enabled }), set: boolSet(func(c *AppConfig, v bool) { c.Exec.Enabled = v })},
	"exec.require_confirm":            {get: boolGet(func(c *AppConfig) bool { return c.Exec.RequireConfirm }), set: boolSet(func(c *AppConfig, v bool) { c.Exec.RequireConfirm = v })},
	"exec.timeout_seconds":            {get: intGet(func(c *AppConfig) int { return c.Exec.TimeoutSeconds }), set: intSet(func(c *AppConfig, v int) { c.Exec.TimeoutSeconds = v })},
	"exec.max_output_bytes":           {get: intGet(func(c *AppConfig) int { return c.Exec.MaxOutputBytes }), set: intSet(func(c *AppConfig, v int) { c.Exec.MaxOutputBytes = v })},
	"exec.working_dir":                {get: strGet(func(c *AppConfig) string { return c.Exec.WorkingDir }), set: strSet(func(c *AppConfig, v string) { c.Exec.WorkingDir = v })},
	"workspace_files.enabled":         {get: wsEnabledGet, set: wsEnabledSet},
	"workspace_files.max_read_bytes":  {get: intGet(func(c *AppConfig) int { return c.WorkspaceFiles.MaxReadBytes }), set: intSet(func(c *AppConfig, v int) { c.WorkspaceFiles.MaxReadBytes = v })},
	"workspace_files.max_write_bytes": {get: intGet(func(c *AppConfig) int { return c.WorkspaceFiles.MaxWriteBytes }), set: intSet(func(c *AppConfig, v int) { c.WorkspaceFiles.MaxWriteBytes = v })},
	"subagent.max_concurrent":         {get: intGet(func(c *AppConfig) int { return c.Subagent.MaxConcurrent }), set: intSet(func(c *AppConfig, v int) { c.Subagent.MaxConcurrent = v })},
	"subagent.default_max_rounds":     {get: intGet(func(c *AppConfig) int { return c.Subagent.DefaultMaxRounds }), set: intSet(func(c *AppConfig, v int) { c.Subagent.DefaultMaxRounds = v })},
	"subagent.max_tool_result_bytes":  {get: intGet(func(c *AppConfig) int { return c.Subagent.MaxToolResultBytes }), set: intSet(func(c *AppConfig, v int) { c.Subagent.MaxToolResultBytes = v })},
	"subagent.max_output_tokens":      {get: intGet(func(c *AppConfig) int { return c.Subagent.MaxOutputTokens }), set: intSet(func(c *AppConfig, v int) { c.Subagent.MaxOutputTokens = v })},
	"subagent.default_tools":          {get: subagentDefaultToolsGet, set: subagentDefaultToolsSet},
	"chat.hard_max_tool_rounds":       {get: intGet(func(c *AppConfig) int { return c.Chat.HardMaxToolRounds }), set: intSet(func(c *AppConfig, v int) { c.Chat.HardMaxToolRounds = v })},
	"mcp.enabled":                     {get: boolGet(func(c *AppConfig) bool { return c.MCP.Enabled }), set: boolSet(func(c *AppConfig, v bool) { c.MCP.Enabled = v })},
	"mcp.tool_timeout_seconds":        {get: intGet(func(c *AppConfig) int { return c.MCP.ToolTimeoutSeconds }), set: intSet(func(c *AppConfig, v int) { c.MCP.ToolTimeoutSeconds = v })},
	"mcp.max_output_bytes":            {get: intGet(func(c *AppConfig) int { return c.MCP.MaxOutputBytes }), set: intSet(func(c *AppConfig, v int) { c.MCP.MaxOutputBytes = v })},
	"mcp.max_exported_tools":          {get: intGet(func(c *AppConfig) int { return c.MCP.MaxExportedTools }), set: intSet(func(c *AppConfig, v int) { c.MCP.MaxExportedTools = v })},
	"mcp.allowed_tools":               {get: mcpAllowedToolsGet, set: mcpAllowedToolsSet},
	"mcp.browser.enabled":             {get: browserBoolGet(func(s *MCPServerEntry) bool { return s.Enabled }), set: browserBoolSet(func(s *MCPServerEntry, v bool) { s.Enabled = v })},
	"mcp.browser.command":             {get: browserStrGet(func(s *MCPServerEntry) string { return s.Command }), set: browserStrSet(func(s *MCPServerEntry, v string) { s.Command = v })},
	"mcp.browser.args":                {get: browserArgsGet, set: browserArgsSet},
}

func strGet(f func(*AppConfig) string) func(*AppConfig) (interface{}, error) {
	return func(c *AppConfig) (interface{}, error) { return f(c), nil }
}

func strSet(f func(*AppConfig, string)) func(*AppConfig, string) error {
	return func(c *AppConfig, v string) error {
		f(c, v)
		return nil
	}
}

func intGet(f func(*AppConfig) int) func(*AppConfig) (interface{}, error) {
	return func(c *AppConfig) (interface{}, error) { return f(c), nil }
}

func intSet(f func(*AppConfig, int)) func(*AppConfig, string) error {
	return func(c *AppConfig, raw string) error {
		v, err := parseInt(raw)
		if err != nil {
			return err
		}
		f(c, v)
		return nil
	}
}

func floatGet(f func(*AppConfig) float64) func(*AppConfig) (interface{}, error) {
	return func(c *AppConfig) (interface{}, error) { return f(c), nil }
}

func floatSet(f func(*AppConfig, float64)) func(*AppConfig, string) error {
	return func(c *AppConfig, raw string) error {
		v, err := parseFloat(raw)
		if err != nil {
			return err
		}
		f(c, v)
		return nil
	}
}

func boolGet(f func(*AppConfig) bool) func(*AppConfig) (interface{}, error) {
	return func(c *AppConfig) (interface{}, error) { return f(c), nil }
}

func boolSet(f func(*AppConfig, bool)) func(*AppConfig, string) error {
	return func(c *AppConfig, raw string) error {
		v, err := parseBool(raw)
		if err != nil {
			return err
		}
		f(c, v)
		return nil
	}
}

func apiKeyGet(c *AppConfig) (interface{}, error) {
	if c.LLM.APIKey != "" {
		return SecretRedacted, nil
	}
	return "", nil
}

func apiKeySet(c *AppConfig, v string) error {
	c.LLM.APIKey = v
	c.LLM.Enabled = v != ""
	return nil
}

func wsEnabledGet(c *AppConfig) (interface{}, error) {
	return c.WorkspaceFilesEnabled(), nil
}

func wsEnabledSet(c *AppConfig, raw string) error {
	v, err := parseBool(raw)
	if err != nil {
		return err
	}
	c.WorkspaceFiles.Enabled = &v
	return nil
}

func mcpAllowedToolsGet(c *AppConfig) (interface{}, error) {
	return strings.Join(c.MCP.AllowedTools, ","), nil
}

func mcpAllowedToolsSet(c *AppConfig, raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		c.MCP.AllowedTools = nil
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	c.MCP.AllowedTools = out
	return nil
}

func subagentDefaultToolsGet(c *AppConfig) (interface{}, error) {
	if len(c.Subagent.DefaultTools) == 0 {
		return "", nil
	}
	return strings.Join(c.Subagent.DefaultTools, ","), nil
}

func subagentDefaultToolsSet(c *AppConfig, raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "*" {
		c.Subagent.DefaultTools = nil
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	c.Subagent.DefaultTools = out
	return nil
}

func browserServer(c *AppConfig) *MCPServerEntry {
	for i := range c.MCP.Servers {
		if c.MCP.Servers[i].Name == "browser" {
			return &c.MCP.Servers[i]
		}
	}
	return nil
}

func ensureBrowserServer(c *AppConfig) *MCPServerEntry {
	if s := browserServer(c); s != nil {
		return s
	}
	c.MCP.Servers = append(c.MCP.Servers, MCPServerEntry{
		Name:    "browser",
		Enabled: true,
		Command: "npx",
		Args:    defaultPlaywrightMCPArgs(),
	})
	return &c.MCP.Servers[len(c.MCP.Servers)-1]
}

func browserStrGet(f func(*MCPServerEntry) string) func(*AppConfig) (interface{}, error) {
	return func(c *AppConfig) (interface{}, error) {
		s := browserServer(c)
		if s == nil {
			return "", nil
		}
		return f(s), nil
	}
}

func browserStrSet(f func(*MCPServerEntry, string)) func(*AppConfig, string) error {
	return func(c *AppConfig, v string) error {
		f(ensureBrowserServer(c), v)
		return nil
	}
}

func browserBoolGet(f func(*MCPServerEntry) bool) func(*AppConfig) (interface{}, error) {
	return func(c *AppConfig) (interface{}, error) {
		s := browserServer(c)
		if s == nil {
			return false, nil
		}
		return f(s), nil
	}
}

func browserBoolSet(f func(*MCPServerEntry, bool)) func(*AppConfig, string) error {
	return func(c *AppConfig, raw string) error {
		v, err := parseBool(raw)
		if err != nil {
			return err
		}
		f(ensureBrowserServer(c), v)
		return nil
	}
}

func browserArgsGet(c *AppConfig) (interface{}, error) {
	s := browserServer(c)
	if s == nil || len(s.Args) == 0 {
		b, _ := json.Marshal(defaultPlaywrightMCPArgs())
		return string(b), nil
	}
	b, err := json.Marshal(s.Args)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func browserArgsSet(c *AppConfig, raw string) error {
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return fmt.Errorf("mcp.browser.args must be a JSON string array: %w", err)
	}
	if len(args) == 0 {
		return fmt.Errorf("mcp.browser.args must not be empty")
	}
	ensureBrowserServer(c).Args = args
	return nil
}

func parseInt(raw string) (int, error) {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("invalid integer value: %q", raw)
	}
	return v, nil
}

func parseFloat(raw string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid float value: %q", raw)
	}
	return v, nil
}

func parseBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean value: %q", raw)
	}
}
