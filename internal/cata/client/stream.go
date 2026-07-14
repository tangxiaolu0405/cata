package client

import (
	"encoding/json"
	"fmt"

	"cata/internal/cata/execcmd"
)

// streamEvent is one NDJSON line from the server during chat.
type streamEvent struct {
	kind string
	raw  map[string]any
	err  error
	done bool
}

func readStreamEvent(s *session) streamEvent {
	const maxGarbageSkips = 8
	skips := 0
	for {
		line, err := s.readLine()
		if err != nil {
			return streamEvent{kind: "io", err: err}
		}
		if len(line) == 0 {
			return streamEvent{kind: "skip"}
		}
		var ev map[string]any
		if err := json.Unmarshal(line, &ev); err != nil {
			skips++
			if skips >= maxGarbageSkips {
				return streamEvent{kind: "io", err: err}
			}
			continue
		}
		k, _ := ev["type"].(string)
		if k == "done" {
			return streamEvent{kind: "done", raw: ev, done: true}
		}
		return streamEvent{kind: k, raw: ev}
	}
}

func execLine(ev map[string]any) string {
	if s, ok := ev["command_line"].(string); ok && s != "" {
		return s
	}
	argv, _ := ev["argv"].([]any)
	var parts []string
	for _, a := range argv {
		if s, ok := a.(string); ok {
			parts = append(parts, s)
		}
	}
	return execcmd.FormatLine(parts)
}

func parseChoiceOptions(raw any) []choiceItem {
	arr, _ := raw.([]any)
	var opts []choiceItem
	for _, r := range arr {
		m, _ := r.(map[string]any)
		opts = append(opts, choiceItem{
			id:    str(m["id"]),
			label: str(m["label"]),
			desc:  str(m["desc"]),
		})
	}
	return opts
}

type choiceItem struct {
	id, label, desc string
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) float64 {
	n, _ := v.(float64)
	return n
}

func trunc(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func formatToolResultLine(kind string, ev map[string]any) string {
	switch kind {
	case "tool_result":
		name := str(ev["name"])
		out := str(ev["output"])
		if out == "" {
			return "  (" + name + " done)"
		}
		return fmt.Sprintf("  %s: %s", name, trunc(out, 400))
	case "exec_done":
		return fmt.Sprintf("$ %s (exit %d)", execLine(ev), int(num(ev["exit_code"])))
	default:
		return ""
	}
}
