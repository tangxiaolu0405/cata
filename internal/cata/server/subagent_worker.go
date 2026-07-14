package server

import (
	"fmt"
	"strings"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
	"cata/internal/llm"
)

func buildWorkerSystemPrompt(task, parentContext string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(brain.LoadWorkerContract()))
	b.WriteString("\n\n")
	if out := strings.TrimSpace(brain.OutputCwd()); out != "" {
		b.WriteString("## Output cwd\n\n`")
		b.WriteString(out)
		b.WriteString("`\n\n")
		b.WriteString("- Use paths **relative to output_cwd** or native paths for the host shell.\n")
		env := brain.ActiveRuntimeEnv()
		if env != nil && !env.ShellSupportsUnixSyntax() {
			b.WriteString("- **Do not** use `/mnt/d/...` WSL paths unless command platform is WSL.\n")
		}
		b.WriteString("- Finish in as few tool rounds as possible.\n\n")
	}
	if ctx := strings.TrimSpace(parentContext); ctx != "" {
		b.WriteString("## Parent context (use as facts; do not re-discover)\n\n")
		b.WriteString(ctx)
		b.WriteString("\n\n")
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
