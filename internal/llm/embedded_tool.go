package llm

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"cata/internal/cata/brain"
)

var (
	reToolCallBlock = regexp.MustCompile(`(?is)<tool_call>\s*(.*?)\s*</tool_call>`)
	reLooseTool     = regexp.MustCompile(`(?is)<tool\s+name="([^"]+)"[^>]*>(.*?)</tool>`)
	reToolName      = regexp.MustCompile(`(?is)<tool\s+name="([^"]+)"`)
	reParamCommand  = regexp.MustCompile(`(?is)<param\s+name="command"\s*>([^<]*)</param>`)
	reParamArgv     = regexp.MustCompile(`(?is)<param\s+name="argv"\s*>([^<]*)</param>`)
	reBracketTool   = regexp.MustCompile(`(?is)\[tool_call\s+([a-zA-Z0-9_]+)\]\s*`)
)

// ParseEmbeddedToolCalls 从 assistant 正文解析嵌入式 tool_call（API 未给 tool_calls 或 arguments 截断时）。
// 支持：[tool_call name] {json}、<tool_call>...</tool_call>、<tool name="...">...</tool>
func ParseEmbeddedToolCalls(content string) (calls []ToolCall, stripped string) {
	if strings.Contains(content, "[tool_call") {
		if calls, stripped := parseBracketToolCalls(content); len(calls) > 0 {
			return calls, stripped
		}
	}
	if strings.Contains(strings.ToLower(content), "<tool") {
		return parseXMLToolCalls(content)
	}
	return nil, content
}

func parseBracketToolCalls(content string) ([]ToolCall, string) {
	var calls []ToolCall
	var kept strings.Builder
	last := 0
	idx := 0
	for _, loc := range reBracketTool.FindAllStringSubmatchIndex(content, -1) {
		kept.WriteString(content[last:loc[0]])
		name := strings.TrimSpace(content[loc[2]:loc[3]])
		obj, ok := extractJSONObjectAt(content, loc[1])
		if !ok {
			last = loc[1]
			continue
		}
		args := NormalizeToolArguments(name, obj)
		if args == "" {
			args = strings.TrimSpace(obj)
			if args == "" {
				last = loc[1]
				continue
			}
		}
		calls = append(calls, ToolCall{
			ID:   fmt.Sprintf("embedded_%d", idx),
			Type: "function",
			Function: ToolCallFunction{
				Name:      name,
				Arguments: args,
			},
		})
		idx++
		end := loc[1] + len(obj)
		if end > len(content) {
			end = len(content)
		}
		last = end
	}
	if len(calls) == 0 {
		return nil, content
	}
	kept.WriteString(content[last:])
	return calls, strings.TrimSpace(kept.String())
}

func parseXMLToolCalls(content string) ([]ToolCall, string) {
	var kept strings.Builder
	last := 0
	idx := 0
	var calls []ToolCall
	for _, loc := range reToolCallBlock.FindAllStringSubmatchIndex(content, -1) {
		kept.WriteString(content[last:loc[0]])
		inner := content[loc[2]:loc[3]]
		if tc, ok := parseToolCallInner(inner, idx, ""); ok {
			calls = append(calls, tc)
			idx++
		}
		last = loc[1]
	}
	if len(calls) == 0 {
		return parseLooseToolBlocks(content)
	}
	kept.WriteString(content[last:])
	stripped := strings.TrimSpace(kept.String())
	return calls, stripped
}

func parseLooseToolBlocks(content string) ([]ToolCall, string) {
	var calls []ToolCall
	var kept strings.Builder
	last := 0
	idx := 0
	for _, loc := range reLooseTool.FindAllStringSubmatchIndex(content, -1) {
		kept.WriteString(content[last:loc[0]])
		name := content[loc[2]:loc[3]]
		inner := content[loc[4]:loc[5]]
		if tc, ok := parseToolCallInner(inner, idx, name); ok {
			calls = append(calls, tc)
			idx++
		}
		last = loc[1]
	}
	if len(calls) == 0 {
		return nil, content
	}
	kept.WriteString(content[last:])
	return calls, strings.TrimSpace(kept.String())
}

func parseToolCallInner(inner string, idx int, toolName string) (ToolCall, bool) {
	name := strings.TrimSpace(toolName)
	if name == "" {
		nm := reToolName.FindStringSubmatch(inner)
		if len(nm) < 2 {
			return ToolCall{}, false
		}
		name = strings.TrimSpace(nm[1])
	}
	var argv []string
	if m := reParamArgv.FindStringSubmatch(inner); len(m) >= 2 {
		raw := strings.TrimSpace(decodeXMLEntities(m[1]))
		if err := json.Unmarshal([]byte(raw), &argv); err != nil {
			var one string
			if json.Unmarshal([]byte(raw), &one) == nil {
				argv = []string{one}
			}
		}
	} else if m := reParamCommand.FindStringSubmatch(inner); len(m) >= 2 {
		argv = shellLineToArgv(strings.TrimSpace(decodeXMLEntities(m[1])))
	}
	if len(argv) > 0 {
		args, _ := json.Marshal(map[string][]string{"argv": argv})
		return ToolCall{
			ID:   fmt.Sprintf("embedded_%d", idx),
			Type: "function",
			Function: ToolCallFunction{
				Name:      name,
				Arguments: string(args),
			},
		}, true
	}
	if params := parseXMLToolParams(inner); len(params) > 0 {
		raw, _ := json.Marshal(params)
		return ToolCall{
			ID:   fmt.Sprintf("embedded_%d", idx),
			Type: "function",
			Function: ToolCallFunction{
				Name:      name,
				Arguments: string(raw),
			},
		}, true
	}
	return ToolCall{}, false
}

var reXMLParam = regexp.MustCompile(`(?is)<param\s+name="([^"]+)"\s*>([^<]*)</param>`)

func parseXMLToolParams(inner string) map[string]interface{} {
	out := make(map[string]interface{})
	for _, m := range reXMLParam.FindAllStringSubmatch(inner, -1) {
		if len(m) < 3 {
			continue
		}
		k := strings.TrimSpace(m[1])
		v := strings.TrimSpace(decodeXMLEntities(m[2]))
		if k == "" {
			continue
		}
		out[k] = v
	}
	return out
}

func shellLineToArgv(line string) []string {
	return brain.ShellLineToArgv(line)
}

func decodeXMLEntities(s string) string {
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", `"`)
	return s
}
