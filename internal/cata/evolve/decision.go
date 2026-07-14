package evolve

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Decision LLM 一轮自主演进决策。
type Decision struct {
	Action   string      `json:"action"`
	Reason   string      `json:"reason"`
	Learning string      `json:"learning"`
	Updates  []DocUpdate `json:"updates"`
}

func parseDecision(raw string) (*Decision, error) {
	jsonText, ok := extractJSONObject(stripCodeFences(raw))
	if !ok {
		return nil, fmt.Errorf("no JSON object in LLM response")
	}

	var d Decision
	if err := json.Unmarshal([]byte(jsonText), &d); err != nil {
		return nil, fmt.Errorf("parse decision JSON: %w", err)
	}
	if d.Action == "" {
		d.Action = "idle"
	}
	return &d, nil
}

func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if idx := strings.Index(s, "\n"); idx >= 0 {
		s = s[idx+1:]
	}
	if idx := strings.LastIndex(s, "```"); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

// extractJSONObject 从首个 { 起按括号匹配提取完整对象；截断或 LastIndex('}') 误切会返回 false。
func extractJSONObject(s string) (string, bool) {
	s = strings.TrimSpace(s)
	start := strings.Index(s, "{")
	if start < 0 {
		return "", false
	}
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}
