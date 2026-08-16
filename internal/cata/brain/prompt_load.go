package brain

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"cata/internal/cata/config"
)

// DelegateTaskToolSpec OpenAI 工具 schema 文案（~/.cata/global/tools/delegate-task-tool.json 或嵌入模板）。
type DelegateTaskToolSpec struct {
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

var promptCache sync.Map // cache key -> string

func loadGlobalPrompt(cacheKey, globalRel, embedPath, fallback string) string {
	if v, ok := promptCache.Load(cacheKey); ok {
		return v.(string)
	}
	s := readGlobalPromptFile(globalRel, embedPath, fallback)
	promptCache.Store(cacheKey, s)
	return s
}

func readGlobalPromptFile(globalRel, embedPath, fallback string) string {
	p := filepath.Join(globalDir(), globalRel)
	if data, err := os.ReadFile(p); err == nil {
		if s := strings.TrimSpace(CompactExcessiveNewlines(string(data))); s != "" {
			return s
		}
	}
	if data, err := embeddedGuidanceFS.ReadFile(embedPath); err == nil {
		if s := strings.TrimSpace(CompactExcessiveNewlines(string(data))); s != "" {
			return s
		}
	}
	return fallback
}

func readGlobalJSON(globalRel, embedPath string) ([]byte, error) {
	p := filepath.Join(globalDir(), globalRel)
	if data, err := os.ReadFile(p); err == nil && len(strings.TrimSpace(string(data))) > 0 {
		return data, nil
	}
	return embeddedGuidanceFS.ReadFile(embedPath)
}

// LoadDelegateGuideBlock 父 Agent 委派准则（~/.cata/global/delegate-guide.md）。
func LoadDelegateGuideBlock() string {
	return loadGlobalPrompt("delegate-guide", FileGlobalDelegateGuide, "guidance/delegate-guide.md",
		"### delegate_task\n\nWorker 为 minimal 脑子；task 须含目标/输入路径/输出验收；context 传数据路径与 schema。")
}

// RenderDelegateGuideBlock 渲染委派指南占位符后注入 full 档路径块（产出区用全局 OutputCwd）。
func RenderDelegateGuideBlock() string {
	return RenderDelegateGuideBlockFor(OutputCwd())
}

// RenderDelegateGuideBlockFor 显式指定产出区的委派指南（多 chat 并行勿依赖全局 OutputCwd）。
func RenderDelegateGuideBlockFor(out string) string {
	max := 4
	if cfg := config.Config; cfg != nil && cfg.Subagent.MaxConcurrent > 0 {
		max = cfg.Subagent.MaxConcurrent
	}
	body := LoadDelegateGuideBlock()
	body = strings.ReplaceAll(body, "{{max_concurrent}}", strconv.Itoa(max))
	body = strings.ReplaceAll(body, "{{csv_path}}", SubagentRunsCSVPath(out))
	return body
}

// SubagentDelegateGuideBlock 兼容旧调用。
func SubagentDelegateGuideBlock() string {
	return RenderDelegateGuideBlock()
}

// SubagentDelegateGuideBlockFor 显式指定产出区的委派指南（多 chat 并行勿依赖全局 OutputCwd）。
func SubagentDelegateGuideBlockFor(out string) string {
	return RenderDelegateGuideBlockFor(out)
}

// LoadDelegateTaskToolSpec delegate_task 工具 description + parameters。
func LoadDelegateTaskToolSpec() (DelegateTaskToolSpec, error) {
	fallback := DelegateTaskToolSpec{
		Description: "Delegate bounded sub-task to minimal-brain worker.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"task":{"type":"string"}},"required":["task"]}`),
	}
	data, err := readGlobalJSON(filepath.Join("tools", FileGlobalDelegateTaskTool), "guidance/delegate-task-tool.json")
	if err != nil {
		return fallback, nil
	}
	var spec DelegateTaskToolSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return fallback, fmt.Errorf("delegate-task-tool.json: %w", err)
	}
	if spec.Description == "" || len(spec.Parameters) == 0 {
		return fallback, nil
	}
	return spec, nil
}

// EnrichWorkerDelegateContext 服务端补全 worker context（动态 cwd/平台 + 父 context；全局回退）。
func EnrichWorkerDelegateContext(parentContext string) string {
	return EnrichWorkerDelegateContextFor(OutputCwd(), ActiveRuntimeEnv(), parentContext)
}

// EnrichWorkerDelegateContextFor 显式指定产出区与运行环境的 worker context 补全（多 chat 并行勿依赖全局）。
func EnrichWorkerDelegateContextFor(out string, env *RuntimeEnv, parentContext string) string {
	var b strings.Builder
	if out = strings.TrimSpace(out); out != "" {
		b.WriteString("output_cwd: `")
		b.WriteString(out)
		b.WriteString("`\n")
	}
	if env != nil {
		b.WriteString(fmt.Sprintf("host/command: %s/%s  shell: %s\n", env.HostPlatform(), env.CommandPlatform(), env.Shell))
		if !env.ShellSupportsUnixSyntax() {
			b.WriteString("paths: use Windows-native or paths relative to output_cwd; avoid /mnt/d/ unless WSL.\n")
		}
	}
	pc := strings.TrimSpace(parentContext)
	if b.Len() > 0 && pc != "" {
		b.WriteString("\n--- parent context ---\n")
	}
	if pc != "" {
		b.WriteString(pc)
	}
	return strings.TrimSpace(b.String())
}
