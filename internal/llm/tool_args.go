package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ParseToolArguments 解析 LLM 返回的 tool arguments（容忍换行/空白不规范 JSON）。
func ParseToolArguments(raw string, dest interface{}) error {
	norm := NormalizeToolArguments("", raw)
	if norm == "" {
		norm = strings.TrimSpace(raw)
		if norm == "" || norm == "null" {
			norm = "{}"
		}
	}
	if err := json.Unmarshal([]byte(norm), dest); err != nil {
		return fmt.Errorf("invalid tool arguments JSON: %w", err)
	}
	return nil
}

// NormalizeToolArguments 将 arguments 规范为合法 JSON 字符串；无法修复时返回空字符串。
func NormalizeToolArguments(toolName, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return "{}"
	}
	if json.Valid([]byte(raw)) {
		return raw
	}
	// 模型常在 JSON 字符串值里塞裸换行（合法 Go string / 非法 JSON）；先转义再验。
	if fixed := escapeRawControlsInJSONStrings(raw); fixed != raw && json.Valid([]byte(fixed)) {
		return fixed
	}
	compact := compactJSONOutsideStrings(raw)
	if json.Valid([]byte(compact)) {
		return compact
	}
	if fixed := escapeRawControlsInJSONStrings(compact); fixed != compact && json.Valid([]byte(fixed)) {
		return fixed
	}
	if fixed := repairToolArgumentsByName(toolName, raw); fixed != "" {
		return fixed
	}
	if toolName != "" {
		if fixed := repairToolArgumentsByName(toolName, compact); fixed != "" {
			return fixed
		}
		if fixed := repairToolArgumentsByName(toolName, escapeRawControlsInJSONStrings(raw)); fixed != "" {
			return fixed
		}
	}
	return ""
}

// escapeRawControlsInJSONStrings 将 JSON 字符串字面量内的裸 \n/\r/\t 转成 \\n 等，使其可被 json.Valid。
func escapeRawControlsInJSONStrings(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 32)
	inStr := false
	esc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if esc {
			b.WriteByte(c)
			esc = false
			continue
		}
		if inStr {
			if c == '\\' {
				b.WriteByte(c)
				esc = true
				continue
			}
			if c == '"' {
				inStr = false
				b.WriteByte(c)
				continue
			}
			switch c {
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				if c < 0x20 {
					b.WriteString(fmt.Sprintf(`\u%04x`, c))
				} else {
					b.WriteByte(c)
				}
			}
			continue
		}
		if c == '"' {
			inStr = true
		}
		b.WriteByte(c)
	}
	return b.String()
}

func repairToolArgumentsByName(name, raw string) string {
	switch name {
	case "append_file":
		path, okP := extractJSONStringField(raw, "path")
		content, okC := extractJSONStringFieldLoose(raw, "content")
		if !okP {
			return ""
		}
		m := map[string]string{"path": path}
		if okC {
			m["content"] = content
		}
		b, err := json.Marshal(m)
		if err != nil {
			return ""
		}
		return string(b)
	case "read_file":
		path, ok := extractJSONStringField(raw, "path")
		if !ok {
			return ""
		}
		b, _ := json.Marshal(map[string]string{"path": path})
		return string(b)
	case "search_replace":
		path, okP := extractJSONStringField(raw, "path")
		if !okP {
			return ""
		}
		m := map[string]string{"path": path}
		if old, ok := extractJSONStringFieldLoose(raw, "old_string"); ok {
			m["old_string"] = old
		}
		if neu, ok := extractJSONStringFieldLoose(raw, "new_string"); ok {
			m["new_string"] = neu
		}
		b, err := json.Marshal(m)
		if err != nil {
			return ""
		}
		return string(b)
	default:
		return ""
	}
}

func extractJSONStringField(raw, key string) (string, bool) {
	needle := `"` + key + `"`
	idx := strings.Index(raw, needle)
	if idx < 0 {
		return "", false
	}
	return scanJSONStringValue(raw, idx+len(needle))
}

// extractJSONStringFieldLoose 允许字符串未闭合（流式截断）。
func extractJSONStringFieldLoose(raw, key string) (string, bool) {
	needle := `"` + key + `"`
	idx := strings.Index(raw, needle)
	if idx < 0 {
		return "", false
	}
	return scanJSONStringValueLoose(raw, idx+len(needle))
}

func scanJSONStringValue(raw string, from int) (string, bool) {
	i := from
	for i < len(raw) && (raw[i] == ' ' || raw[i] == ':' || raw[i] == '\t' || raw[i] == '\n' || raw[i] == '\r') {
		i++
	}
	if i >= len(raw) || raw[i] != '"' {
		return "", false
	}
	s, ok := readJSONStringLiteral(raw, i+1)
	if !ok {
		return "", false
	}
	return s, true
}

func scanJSONStringValueLoose(raw string, from int) (string, bool) {
	i := from
	for i < len(raw) && (raw[i] == ' ' || raw[i] == ':' || raw[i] == '\t' || raw[i] == '\n' || raw[i] == '\r') {
		i++
	}
	if i >= len(raw) || raw[i] != '"' {
		return "", false
	}
	s, _ := readJSONStringLiteralLoose(raw, i+1)
	if s == "" {
		return "", false
	}
	return s, true
}

func readJSONStringLiteral(raw string, start int) (string, bool) {
	var b strings.Builder
	escape := false
	for j := start; j < len(raw); j++ {
		c := raw[j]
		if escape {
			b.WriteByte(c)
			escape = false
			continue
		}
		if c == '\\' {
			escape = true
			continue
		}
		if c == '"' {
			return decodeJSONStringContent(b.String()), true
		}
		b.WriteByte(c)
	}
	return "", false
}

func readJSONStringLiteralLoose(raw string, start int) (string, bool) {
	var b strings.Builder
	escape := false
	for j := start; j < len(raw); j++ {
		c := raw[j]
		if escape {
			b.WriteByte(c)
			escape = false
			continue
		}
		if c == '\\' {
			escape = true
			continue
		}
		if c == '"' {
			return decodeJSONStringContent(b.String()), true
		}
		b.WriteByte(c)
	}
	if b.Len() == 0 {
		return "", false
	}
	return decodeJSONStringContent(b.String()), true
}

func decodeJSONStringContent(s string) string {
	wrapped := `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	var out string
	if err := json.Unmarshal([]byte(wrapped), &out); err == nil {
		return out
	}
	return s
}

func compactJSONOutsideStrings(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	escape := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escape {
			b.WriteByte(c)
			escape = false
			continue
		}
		if inString {
			if c == '\\' {
				escape = true
				b.WriteByte(c)
				continue
			}
			if c == '"' {
				inString = false
			}
			b.WriteByte(c)
			continue
		}
		if c == '"' {
			inString = true
			b.WriteByte(c)
			continue
		}
		if c == ' ' || c == '\n' || c == '\r' || c == '\t' {
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// SanitizeMessagesToolCalls 修复 history 中 assistant.tool_calls 的非法 arguments，避免 API 400。
func SanitizeMessagesToolCalls(msgs []Message) []Message {
	if len(msgs) == 0 {
		return msgs
	}
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if len(out[i].ToolCalls) == 0 {
			continue
		}
		fixed := make([]ToolCall, 0, len(out[i].ToolCalls))
		for _, tc := range out[i].ToolCalls {
			norm := NormalizeToolArguments(tc.Function.Name, tc.Function.Arguments)
			if norm == "" {
				continue
			}
			tc.Function.Arguments = norm
			fixed = append(fixed, tc)
		}
		out[i].ToolCalls = fixed
		if len(fixed) == 0 && strings.TrimSpace(out[i].Content) == "" {
			out[i].Content = "(tool call omitted: invalid arguments)"
		}
	}
	return out
}

// NormalizeToolCalls 就地修复一批 tool_calls（流式聚合后调用）。
func NormalizeToolCalls(calls []ToolCall) []ToolCall {
	if len(calls) == 0 {
		return calls
	}
	out := make([]ToolCall, 0, len(calls))
	for _, tc := range calls {
		norm := NormalizeToolArguments(tc.Function.Name, tc.Function.Arguments)
		if norm == "" {
			continue
		}
		tc.Function.Arguments = norm
		out = append(out, tc)
	}
	return out
}

// extractJSONObjectAt 从 pos 起跳过空白后读取一个 JSON 对象（允许截断无闭合 }）。
func extractJSONObjectAt(s string, pos int) (string, bool) {
	i := pos
	for i < len(s) && (s[i] == ' ' || s[i] == '\n' || s[i] == '\r' || s[i] == '\t') {
		i++
	}
	if i >= len(s) || s[i] != '{' {
		return "", false
	}
	depth := 0
	inStr := false
	esc := false
	start := i
	for j := i; j < len(s); j++ {
		c := s[j]
		if esc {
			esc = false
			continue
		}
		if inStr {
			if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			continue
		}
		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				return s[start : j+1], true
			}
		}
	}
	if depth > 0 {
		return s[start:], true
	}
	return "", false
}
