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
	reBracketTool   = regexp.MustCompile(`(?is)\[tool_call\s+([a-zA-Z0-9_]+)\]\s*`)
	// 唯一保留的 XML 方言：<function=name>…<parameter=key>…</parameter>…</function>
	reFunctionEq  = regexp.MustCompile(`(?is)<function=([a-zA-Z0-9_]+)>\s*(.*?)\s*</function>`)
	reParameterEq = regexp.MustCompile(`(?is)<parameter=([a-zA-Z0-9_]+)>\s*(.*?)\s*</parameter>`)
)

// ParseEmbeddedToolCalls 从 assistant 正文解析嵌入式 tool_call（API 未给 tool_calls 或 arguments 截断时）。
// 仅支持两种兜底：
//  1. [tool_call name] {json}
//  2. <function=name>…<parameter=key>…</parameter>…</function>（可包在 <tool_call> 内）
func ParseEmbeddedToolCalls(content string) (calls []ToolCall, stripped string) {
	if strings.Contains(content, "[tool_call") {
		if calls, stripped := parseBracketToolCalls(content); len(calls) > 0 {
			return calls, stripped
		}
	}
	lower := strings.ToLower(content)
	if strings.Contains(lower, "<tool_call") || strings.Contains(lower, "<function") {
		return parseFunctionEqToolCalls(content)
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

func parseFunctionEqToolCalls(content string) ([]ToolCall, string) {
	var kept strings.Builder
	last := 0
	idx := 0
	var calls []ToolCall
	for _, loc := range reToolCallBlock.FindAllStringSubmatchIndex(content, -1) {
		kept.WriteString(content[last:loc[0]])
		inner := content[loc[2]:loc[3]]
		calls = append(calls, collectFunctionEqCalls(inner, &idx)...)
		last = loc[1]
	}
	if len(calls) == 0 {
		return parseFunctionEqBlocks(content)
	}
	kept.WriteString(content[last:])
	return calls, strings.TrimSpace(kept.String())
}

func parseFunctionEqBlocks(content string) ([]ToolCall, string) {
	var calls []ToolCall
	var kept strings.Builder
	last := 0
	idx := 0
	for _, loc := range reFunctionEq.FindAllStringSubmatchIndex(content, -1) {
		kept.WriteString(content[last:loc[0]])
		name := content[loc[2]:loc[3]]
		body := content[loc[4]:loc[5]]
		if tc, ok := buildEmbeddedToolCall(name, body, idx); ok {
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

func collectFunctionEqCalls(content string, idx *int) []ToolCall {
	var calls []ToolCall
	for _, m := range reFunctionEq.FindAllStringSubmatch(content, -1) {
		if len(m) < 3 {
			continue
		}
		if tc, ok := buildEmbeddedToolCall(m[1], m[2], *idx); ok {
			calls = append(calls, tc)
			*idx++
		}
	}
	return calls
}

func buildEmbeddedToolCall(name, body string, idx int) (ToolCall, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ToolCall{}, false
	}
	var argv []string
	if m := reParameterEqNamed(body, "argv"); m != "" {
		raw := strings.TrimSpace(decodeXMLEntities(m))
		if err := json.Unmarshal([]byte(raw), &argv); err != nil {
			var one string
			if json.Unmarshal([]byte(raw), &one) == nil {
				argv = []string{one}
			}
		}
	} else if m := reParameterEqNamed(body, "command"); m != "" {
		argv = brain.ShellLineToArgv(strings.TrimSpace(decodeXMLEntities(m)))
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
	params := parseParameterEqParams(body)
	argsJSON := "{}"
	if len(params) > 0 {
		raw, err := json.Marshal(params)
		if err != nil {
			return ToolCall{}, false
		}
		argsJSON = string(raw)
	}
	return ToolCall{
		ID:   fmt.Sprintf("embedded_%d", idx),
		Type: "function",
		Function: ToolCallFunction{
			Name:      name,
			Arguments: argsJSON,
		},
	}, true
}

func parseParameterEqParams(inner string) map[string]interface{} {
	out := make(map[string]interface{})
	for _, m := range reParameterEq.FindAllStringSubmatch(inner, -1) {
		if len(m) < 3 {
			continue
		}
		k := strings.TrimSpace(m[1])
		if k == "" || k == "argv" || k == "command" {
			continue
		}
		out[k] = strings.TrimSpace(decodeXMLEntities(m[2]))
	}
	return out
}

func reParameterEqNamed(inner, key string) string {
	for _, m := range reParameterEq.FindAllStringSubmatch(inner, -1) {
		if len(m) >= 3 && strings.EqualFold(strings.TrimSpace(m[1]), key) {
			return m[2]
		}
	}
	return ""
}

func decodeXMLEntities(s string) string {
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", `"`)
	return s
}
