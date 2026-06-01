package client

import "strings"

type cmdDef struct {
	Name    string
	Aliases []string
	Desc    string
}

var slashCommands = []cmdDef{
	{Name: "config", Desc: "show config path"},
	{Name: "exit", Aliases: []string{"quit", "q"}, Desc: "exit cata"},
	{Name: "clear", Aliases: []string{"reset"}, Desc: "reset chat session"},
	{Name: "cls", Desc: "clear chat log view"},
	{Name: "help", Desc: "show available commands"},
	{Name: "status", Desc: "session status"},
	{Name: "retry", Desc: "retry last message"},
}

func matchSlashCmds(prefix string) []cmdDef {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	var result []cmdDef
	for _, c := range slashCommands {
		if strings.HasPrefix(strings.ToLower(c.Name), prefix) {
			result = append(result, c)
			continue
		}
		for _, a := range c.Aliases {
			if strings.HasPrefix(strings.ToLower(a), prefix) {
				result = append(result, c)
				break
			}
		}
	}
	return result
}

func slashCommonPrefix(a, b string) string {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return a[:n]
}

// tabCompleteSlash completes the first line when it starts with "/".
func tabCompleteSlash(val string) string {
	first, rest := firstInputLine(val)
	if !strings.HasPrefix(first, "/") {
		return val
	}
	prefix := first[1:]
	matches := matchSlashCmds(prefix)
	if len(matches) == 0 {
		return val
	}
	if len(matches) == 1 {
		return "/" + matches[0].Name + " " + rest
	}
	common := matches[0].Name
	for _, m := range matches[1:] {
		common = slashCommonPrefix(common, m.Name)
	}
	if len(common) > len(prefix) {
		return "/" + common + rest
	}
	return val
}

func firstInputLine(val string) (first, rest string) {
	if i := strings.Index(val, "\n"); i >= 0 {
		return val[:i], val[i:]
	}
	return val, ""
}

// slashLineComplete 首行已是完整 / 命令名（或别名），Enter 应执行而非再补全。
func slashLineComplete(val string) bool {
	first, _ := firstInputLine(val)
	trimmed := strings.TrimSpace(first)
	if !strings.HasPrefix(trimmed, "/") {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(trimmed, "/"))
	if token == "" {
		return false
	}
	for _, c := range slashCommands {
		if strings.EqualFold(c.Name, token) {
			return true
		}
		for _, a := range c.Aliases {
			if strings.EqualFold(a, token) {
				return true
			}
		}
	}
	return false
}
