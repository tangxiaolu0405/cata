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

// ContextTier 主 chat 按需加载的工具与 system 节选档位（单一决策轴）。
type ContextTier int

const (
	ContextTierLight ContextTier = iota
	ContextTierStandard
	ContextTierFull
)

func (t ContextTier) String() string {
	switch t {
	case ContextTierLight:
		return "light"
	case ContextTierStandard:
		return "standard"
	default:
		return "full"
	}
}

// PromptProfile 返回与该档位对齐的 system 节选档位（主 chat 不使用 minimal）。
func (t ContextTier) PromptProfile() brain.PromptProfile {
	switch t {
	case ContextTierFull:
		return brain.PromptProfileFull
	default:
		return brain.PromptProfileTask
	}
}

// 各档位内置工具（MCP 仅 full 档追加）。
var (
	contextTierLightNames = []string{"read_file", "list_files", "read_skill", "declare_task", "list_modes"}
	contextTierStdExtra   = []string{"search_replace", "append_file", "create_file", "run_command", "run_skill", "ask_user",
		"delegate_task", "delegate_mode", "case_artifact", "delegate_wait"}
	contextTierFullExtra = []string{}
)

const readOnlyQAMaxRunes = 200

var (
	reCodeFence = regexp.MustCompile("```")
	reURL       = regexp.MustCompile(`https?://[^\s\]\)>"']+`)
	reFileExt   = regexp.MustCompile(`(?i)(?:^|[\s"'(\[{])([\w./\\@-]+)\.(go|ts|tsx|js|jsx|py|rs|java|cs|cpp|c|h|hpp|md|json|yaml|yml|toml|xml|html|css|sh|bash|bat|ps1|sql|vue|svelte)\b`)
	rePathSep   = regexp.MustCompile(`(?:^|[\s"'(\[{])([\w.-]+[/\\]){1,}[\w.-]+`)
	reShellMeta = regexp.MustCompile(`&&|\|\||\$\(|(?:^|[\s;])(?:sudo|npm|pnpm|yarn|cargo|make|cmake|docker|kubectl|git)\b`)
)

// InferContextTier 选择主 chat 上下文档位（工具集 + system 节选）。
//
// 原则：任务形态不可枚举，不做关键词猜测。
//   - 有 tool 活动或 round≥2 → full（含 MCP）
//   - 项目有专职 modes → 至少 standard（保证委派工具可用）
//   - 首轮默认 standard（读写+命令+delegate）
//   - 仅极高置信「纯问句、无结构信号、无专职 mode」→ light
//   - MCP 已启用且用户给出 URL → full（可能需要 browser）
func InferContextTier(ws *brain.Workspace, round int, history []llm.Message, userText string) ContextTier {
	if round > 1 || historyHasToolActivity(history) {
		return ContextTierFull
	}
	if mcpEnabled() && reURL.MatchString(userText) {
		return ContextTierFull
	}
	if brain.HasSpecialistModes(ws) {
		return ContextTierStandard
	}
	if isHighConfidenceReadOnlyQA(userText) {
		return ContextTierLight
	}
	return ContextTierStandard
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

func tierToolNames(tier ContextTier) []string {
	switch tier {
	case ContextTierLight:
		return append([]string(nil), contextTierLightNames...)
	case ContextTierStandard:
		out := append([]string(nil), contextTierLightNames...)
		out = append(out, contextTierStdExtra...)
		return out
	default:
		out := append([]string(nil), contextTierLightNames...)
		out = append(out, contextTierStdExtra...)
		out = append(out, contextTierFullExtra...)
		return out
	}
}

func (ss *SocketServer) buildTerminalChatToolsForTier(tier ContextTier, outCwd string, runtime *brain.RuntimeEnv) []llm.Tool {
	key := ss.chatToolsCacheKey() + "|tier:" + tier.String()
	if outCwd != "" {
		key += "|out:" + outCwd
	}
	if runtime != nil {
		key += "|env:" + runtime.OS + "/" + runtime.Shell
	}
	if key == ss.chatToolsKey && len(ss.chatToolsCache) > 0 {
		out := make([]llm.Tool, len(ss.chatToolsCache))
		copy(out, ss.chatToolsCache)
		return out
	}
	if tier == ContextTierFull {
		mcp.EnsureInit()
	}
	// 按 tier 顺序逐个取 schema：run_command 的说明依赖产出区/运行环境，必须用显式 out/env 重建，
	// 不能走全局 RunCommandToolDescription()（多 chat 并行时全局 OutputCwd/RuntimeEnv 可能已被其它会话改写）。
	var out []llm.Tool
	for _, name := range tierToolNames(tier) {
		t, ok := ss.tools.Get(name)
		if !ok {
			continue
		}
		schema := t.Schema()
		if name == "run_command" {
			schema.Function.Description = brain.RunCommandToolDescriptionFor(runtime, outCwd)
		}
		out = append(out, schema)
	}
	if tier == ContextTierFull {
		if mgr := mcp.Global(); mgr != nil {
			out = append(out, mgr.Tools()...)
		}
	}
	ss.chatToolsKey = key
	ss.chatToolsCache = out
	return out
}
