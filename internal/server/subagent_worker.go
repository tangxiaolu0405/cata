package server

import (
	"fmt"
	"strings"

	"cata/internal/brain"
	"cata/internal/config"
	"cata/internal/llm"
)

const workerSummaryFormat = `STATUS: ok|failed|partial
RESULT: <what was done or found>
ARTIFACTS: <paths/outputs changed, or "none">
NOTES: <blockers/assumptions, or "none">`

func buildWorkerSystemPrompt(task, parentContext string) string {
	var b strings.Builder
	b.WriteString("You are a Cata **worker** sub-agent: execute ONE bounded task at **low cost**.\n\n")
	b.WriteString("## Role\n\n")
	b.WriteString("- Parent owns planning and integration; you **execute only** what the task states.\n")
	b.WriteString("- No ask_user, delegate_task, or scope expansion.\n")
	b.WriteString("- Prefer deterministic steps: exact paths, explicit commands, minimal tool rounds.\n")
	b.WriteString("- **Do not run browser/MCP tools in parallel with other workers** (single browser session).\n")
	b.WriteString("- cwd / exec confirm / timeouts match the parent chat.\n\n")
	b.WriteString("## Done criteria\n\n")
	b.WriteString("When finished, stop calling tools and reply using exactly this block:\n\n")
	b.WriteString(workerSummaryFormat)
	b.WriteString("\n\n")
	if ctx := strings.TrimSpace(parentContext); ctx != "" {
		b.WriteString("## Parent context (use as facts; do not re-discover)\n\n")
		b.WriteString(ctx)
		b.WriteString("\n\n")
	}
	if out := strings.TrimSpace(brain.OutputCwd()); out != "" {
		b.WriteString("## Output cwd\n\n`")
		b.WriteString(out)
		b.WriteString("`\n\n")
	}
	b.WriteString("## Task\n\n")
	b.WriteString(strings.TrimSpace(task))
	return b.String()
}

func filterWorkerTools(all []llm.Tool, allow []string) ([]llm.Tool, error) {
	if len(allow) == 0 {
		return all, nil
	}
	want := make(map[string]bool, len(allow))
	for _, name := range allow {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		want[name] = true
	}
	if len(want) == 0 {
		return all, nil
	}
	var out []llm.Tool
	for _, t := range all {
		if want[t.Function.Name] {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("delegate_task: tools filter matched no worker tools")
	}
	return out, nil
}

func truncateWorkerToolResult(out string, maxBytes int) string {
	if maxBytes <= 0 || len(out) <= maxBytes {
		return out
	}
	return out[:maxBytes] + "\n…(truncated for worker context)"
}

func workerToolResultMaxBytes() int {
	if config.Config != nil && config.Config.Subagent.MaxToolResultBytes > 0 {
		return config.Config.Subagent.MaxToolResultBytes
	}
	return 8192
}
