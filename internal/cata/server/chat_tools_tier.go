package server

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
	"cata/internal/llm"
	"cata/internal/mcp"
)

// ToolTier 主 chat 按需加载的工具档位。
type ToolTier int

const (
	ToolTierLight ToolTier = iota
	ToolTierStandard
	ToolTierFull
)

func (t ToolTier) String() string {
	switch t {
	case ToolTierLight:
		return "light"
	case ToolTierStandard:
		return "standard"
	default:
		return "full"
	}
}

// 各档位内置工具（MCP 仅 full 档追加）。
var (
	toolTierLightNames = []string{"read_file", "list_files", "read_skill", "declare_task"}
	toolTierStdExtra   = []string{"search_replace", "append_file", "create_file", "run_command", "run_skill", "ask_user"}
	toolTierFullExtra  = []string{"delegate_task", "delegate_wait"}
)

const readOnlyQAMaxRunes = 200

var (
	reCodeFence   = regexp.MustCompile("```")
	reURL         = regexp.MustCompile(`https?://[^\s\]\)>"']+`)
	reFileExt     = regexp.MustCompile(`(?i)(?:^|[\s"'(\[{])([\w./\\@-]+)\.(go|ts|tsx|js|jsx|py|rs|java|cs|cpp|c|h|hpp|md|json|yaml|yml|toml|xml|html|css|sh|bash|bat|ps1|sql|vue|svelte)\b`)
	rePathSep     = regexp.MustCompile(`(?:^|[\s"'(\[{])([\w.-]+[/\\]){1,}[\w.-]+`)
	reShellMeta   = regexp.MustCompile(`&&|\|\||\$\(|(?:^|[\s;])(?:sudo|npm|pnpm|yarn|cargo|make|cmake|docker|kubectl|git)\b`)
)

// InferToolTier 选择工具档位。
//
// 原则：任务形态不可枚举，不做关键词猜测。
//   - 有 tool 活动或 round≥2 → full（含 MCP / delegate）
//   - 首轮默认 standard（读写+命令，覆盖绝大多数仓库内任务）
//   - 仅极高置信「纯问句、无结构信号」→ light（省 token）
//   - MCP 已启用且用户给出 URL → full（可能需要 browser）
func InferToolTier(round int, history []llm.Message, userText string) ToolTier {
	if round > 1 || historyHasToolActivity(history) {
		return ToolTierFull
	}
	if mcpEnabled() && reURL.MatchString(userText) {
		return ToolTierFull
	}
	if isHighConfidenceReadOnlyQA(userText) {
		return ToolTierLight
	}
	return ToolTierStandard
}

func historyHasToolActivity(history []llm.Message) bool {
	for _, m := range history {
		if m.Role == "tool" || len(m.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

func mcpEnabled() bool {
	cfg := config.Config
	return cfg != nil && cfg.MCP.Enabled
}

// hasStructuralTaskSignals 消息里是否带有「可能要动仓库/命令/外链」的结构特征（非自然语言关键词）。
func hasStructuralTaskSignals(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	if reCodeFence.MatchString(t) {
		return true
	}
	if reFileExt.MatchString(t) {
		return true
	}
	if rePathSep.MatchString(t) {
		return true
	}
	if reURL.MatchString(t) {
		return true
	}
	if reShellMeta.MatchString(t) {
		return true
	}
	return false
}

// isHighConfidenceReadOnlyQA 极窄：短问句 + 无结构信号。拿不准一律不走 light。
func isHighConfidenceReadOnlyQA(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	if utf8.RuneCountInString(t) > readOnlyQAMaxRunes {
		return false
	}
	if hasStructuralTaskSignals(t) {
		return false
	}
	return strings.HasSuffix(t, "?") || strings.HasSuffix(t, "？")
}

func tierToolNames(tier ToolTier) []string {
	switch tier {
	case ToolTierLight:
		return append([]string(nil), toolTierLightNames...)
	case ToolTierStandard:
		out := append([]string(nil), toolTierLightNames...)
		out = append(out, toolTierStdExtra...)
		return out
	default:
		out := append([]string(nil), toolTierLightNames...)
		out = append(out, toolTierStdExtra...)
		out = append(out, toolTierFullExtra...)
		return out
	}
}

func (ss *SocketServer) buildTerminalChatToolsForTier(tier ToolTier) []llm.Tool {
	key := ss.chatToolsCacheKey() + "|tier:" + tier.String()
	if key == ss.chatToolsKey && len(ss.chatToolsCache) > 0 {
		out := make([]llm.Tool, len(ss.chatToolsCache))
		copy(out, ss.chatToolsCache)
		return out
	}
	if tier == ToolTierFull {
		mcp.EnsureInit()
	}
	allow := make(map[string]struct{}, len(tierToolNames(tier)))
	for _, n := range tierToolNames(tier) {
		allow[n] = struct{}{}
	}
	var out []llm.Tool
	for _, schema := range ss.tools.Schemas() {
		if _, ok := allow[schema.Function.Name]; ok {
			out = append(out, schema)
		}
	}
	if tier == ToolTierFull {
		if mgr := mcp.Global(); mgr != nil {
			out = append(out, mgr.Tools()...)
		}
	}
	ss.chatToolsKey = key
	ss.chatToolsCache = out
	return out
}

// PromptProfileForTier 与工具档位对齐的 system 节选档位（主 chat 不使用 minimal）。
func PromptProfileForTier(tier ToolTier) brain.PromptProfile {
	switch tier {
	case ToolTierFull:
		return brain.PromptProfileFull
	default:
		return brain.PromptProfileTask
	}
}
