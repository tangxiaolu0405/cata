package llm

import (
	"os"
	"strings"

	"cata/internal/cata/brain"
)

// 发往 LLM 的提示词由下列部分在 HTTP 出站前组装（见 withBootLeaderSystemMessage / buildHTTPChatRequest）。
// 终端 cata chat 的 history 仅含 user / assistant / tool；①② 由客户端注入，工具走 tools 字段。
const (
	PromptPartBootLeader   = "boot-leader"   // ① system：global/boot-assembler 或 boot-leader（每次请求注入）
	PromptPartBrainExcerpt = "brain-excerpt" // ② system：路径块 + ~/.cata 引导 + 项目 .cata 内容节选
	PromptPartConversation = "conversation"  // user / assistant / tool 多轮历史（调用方传入）
	PromptPartTools        = "tools"         // OpenAI tools JSON（同请求，非 messages）
	PromptPartAPIParams    = "api-params"    // model、max_tokens、temperature、tool_choice
)

const (
	llmLogPreviewRunes = 240
	llmLogFullTailMsgs = 3 // 非 verbose 时，conversation 末尾几条保留全文
)

func llmLogVerbose() bool {
	v := strings.TrimSpace(os.Getenv("LLM_LOG_VERBOSE"))
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "full")
}

func runeLen(s string) int {
	return len([]rune(s))
}

func previewRunes(s string, max int) string {
	if max <= 0 || s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// promptComponentLog 单段提示词在日志中的摘要（默认不含全文，避免与 API 载荷重复刷盘）。
type promptComponentLog struct {
	ID      string `json:"id"`
	Role    string `json:"role,omitempty"`
	Source  string `json:"source,omitempty"`
	Chars   int    `json:"chars"`
	Preview string `json:"preview,omitempty"`
	Content string `json:"content,omitempty"`
	// 仅 conversation / tools
	Name       string   `json:"name,omitempty"`
	ToolCallID string   `json:"tool_call_id,omitempty"`
	ToolNames  []string `json:"tool_names,omitempty"`
	Count      int      `json:"count,omitempty"`
}

// buildPromptManifest 将「实际发往 API 的 messages + tools」拆解为组件清单（供 llm.log 默认格式）。
func buildPromptManifest(effective []Message, tools []Tool, toolChoice string, maxTokens int, temperature float64) map[string]interface{} {
	boot := strings.TrimSpace(loadBootLeaderPrompt())
	var static []promptComponentLog
	var conv []promptComponentLog
	convStart := 0

	for i, m := range effective {
		c := strings.TrimSpace(m.Content)
		ch := runeLen(c)
		switch {
		case m.Role == "system" && boot != "" && c == boot:
			static = append(static, promptComponentLog{
				ID: PromptPartBootLeader, Role: "system",
				Source: "brain/boot-leader.md", Chars: ch,
				Preview: previewRunes(c, llmLogPreviewRunes),
			})
			convStart = i + 1
		case m.Role == "system" && (strings.HasPrefix(c, brain.TerminalPathsSystemPrefix) ||
			brain.BrainExcerptInjected(c)):
			static = append(static, promptComponentLog{
				ID: PromptPartBrainExcerpt, Role: "system",
				Source: "~/.cata/global + focus_path/.cata", Chars: ch,
				Preview: previewRunes(c, llmLogPreviewRunes),
			})
			convStart = i + 1
		case m.Role == "system":
			static = append(static, promptComponentLog{
				ID: "system-other", Role: "system", Chars: ch,
				Preview: previewRunes(c, llmLogPreviewRunes),
			})
			convStart = i + 1
		}
	}

	msgs := effective
	if convStart < len(msgs) {
		msgs = msgs[convStart:]
	}
	tailFullFrom := 0
	if len(msgs) > llmLogFullTailMsgs {
		tailFullFrom = len(msgs) - llmLogFullTailMsgs
	}
	for i, m := range msgs {
		c := strings.TrimSpace(m.Content)
		ch := runeLen(c)
		entry := promptComponentLog{
			ID: PromptPartConversation, Role: m.Role, Chars: ch,
			Name: m.Name, ToolCallID: m.ToolCallID,
		}
		fullBody := i >= tailFullFrom
		if fullBody {
			entry.Content = c
		} else {
			entry.Preview = previewRunes(c, llmLogPreviewRunes)
		}
		if len(m.ToolCalls) > 0 {
			names := make([]string, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				names = append(names, tc.Function.Name)
			}
			entry.ToolNames = names
			if fullBody {
				entry.Content = summarizeAssistantToolCalls(m)
			}
		}
		conv = append(conv, entry)
	}

	toolNames := make([]string, 0, len(tools))
	toolsChars := 0
	for _, t := range tools {
		toolNames = append(toolNames, t.Function.Name)
		toolsChars += runeLen(t.Function.Description)
		toolsChars += len(t.Function.Parameters)
	}

	return map[string]interface{}{
		"components": map[string]interface{}{
			PromptPartBootLeader:   pickStatic(static, PromptPartBootLeader),
			PromptPartBrainExcerpt: pickStatic(static, PromptPartBrainExcerpt),
			"system_other":         pickStaticOthers(static),
			PromptPartConversation: conv,
			PromptPartTools: promptComponentLog{
				ID: PromptPartTools, Count: len(tools), Chars: toolsChars, ToolNames: toolNames,
			},
		},
		PromptPartAPIParams: map[string]interface{}{
			"max_tokens": maxTokens, "temperature": temperature, "tool_choice": toolChoice,
		},
		"totals": map[string]int{
			"message_chars": runeLenMessages(effective),
			"tools_chars":   toolsChars,
		},
	}
}

func pickStatic(static []promptComponentLog, id string) interface{} {
	for _, s := range static {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func pickStaticOthers(static []promptComponentLog) []promptComponentLog {
	var out []promptComponentLog
	for _, s := range static {
		if s.ID == "system-other" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func runeLenMessages(msgs []Message) int {
	n := 0
	for _, m := range msgs {
		n += runeLen(m.Content)
		for _, tc := range m.ToolCalls {
			n += runeLen(tc.Function.Arguments)
		}
	}
	return n
}

func summarizeAssistantToolCalls(m Message) string {
	if len(m.ToolCalls) == 0 {
		return m.Content
	}
	var b strings.Builder
	if strings.TrimSpace(m.Content) != "" {
		b.WriteString(m.Content)
		b.WriteByte('\n')
	}
	for _, tc := range m.ToolCalls {
		b.WriteString("[tool_call ")
		b.WriteString(tc.Function.Name)
		b.WriteString("] ")
		b.WriteString(tc.Function.Arguments)
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}
