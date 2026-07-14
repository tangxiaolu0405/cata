package execcmd

import (
	"strconv"
	"strings"
)

// FormatLine 将 argv 格式化为一条可读的 shell 风格命令行（参数含空白时加引号）。
func FormatLine(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = quoteArg(a)
	}
	return strings.Join(parts, " ")
}

func quoteArg(s string) string {
	if s == "" {
		return `""`
	}
	needsQuote := false
	for _, r := range s {
		if r <= ' ' || r == '"' || r == '\'' || r == '\\' {
			needsQuote = true
			break
		}
	}
	if needsQuote {
		return strconv.Quote(s)
	}
	return s
}
