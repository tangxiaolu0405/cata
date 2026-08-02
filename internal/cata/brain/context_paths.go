package brain

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"cata/internal/cata/config"
)

// TerminalPathsSystemPrefix 注入 LLM 的路径约定 system 消息前缀（与 llm.log 识别一致）。
const TerminalPathsSystemPrefix = "【Cata 路径：脑子与产出区】"

var (
	outputMu       sync.RWMutex
	activeOutputCwd string
)

// SetOutputCwd 设置当前请求的产出区目录（cata chat 的 cwd）。
func SetOutputCwd(cwd string) {
	outputMu.Lock()
	activeOutputCwd = strings.TrimSpace(cwd)
	outputMu.Unlock()
}

// OutputCwd 返回当前产出区路径。
func OutputCwd() string {
	outputMu.RLock()
	defer outputMu.RUnlock()
	return activeOutputCwd
}

// TerminalPathsSystemBlock 每轮对话注入的动态路径说明（脑子 vs 产出区）。
func TerminalPathsSystemBlock() string {
	return TerminalPathsSystemBlockFor(ActivePromptProfile())
}

// TerminalPathsSystemBlockFor 按指定 profile 生成路径块（worker 等并发场景勿依赖全局 Active）。
func TerminalPathsSystemBlockFor(p PromptProfile) string {

	switch ProfileRank(p) {
	case 0:
		return terminalPathsSystemBlockMinimal()
	case 1:
		return terminalPathsSystemBlockTask()
	default:
		return terminalPathsSystemBlockFull()
	}
}

func terminalPathsSystemBlockMinimal() string {
	out := OutputCwd()
	var b strings.Builder
	b.WriteString(TerminalPathsSystemPrefix)
	b.WriteString("（简）\n")
	if w := Active(); w != nil {
		b.WriteString("- focus_path：`")
		b.WriteString(w.RootPath)
		b.WriteString("`\n")
	}
	if out != "" {
		b.WriteString("- output_cwd：`")
		b.WriteString(out)
		b.WriteString("`\n")
	}
	if env := ActiveRuntimeEnv(); env != nil {
		b.WriteString(fmt.Sprintf("- host/command：`%s`/`%s`  shell：`%s`\n",
			env.HostPlatform(), env.CommandPlatform(), env.Shell))
	}
	b.WriteString("- 工具见 **tools[]**。\n")
	return b.String()
}

func terminalPathsSystemBlockTask() string {
	home := CataHome()
	out := OutputCwd()
	var b strings.Builder
	b.WriteString(TerminalPathsSystemPrefix)
	b.WriteString("（task）\n")
	b.WriteString("三根：CATA_HOME / 项目 `.cata` / 产出区。默认=产出区；`brain/…`→项目 `.cata` 或 home 记忆；`global/…`→`")
	b.WriteString(home)
	b.WriteString("/global/`。\n")
	if w := Active(); w != nil {
		b.WriteString("- focus_path：`")
		b.WriteString(w.RootPath)
		b.WriteString("`\n- 项目 `.cata`：`")
		b.WriteString(w.ProjectCataRoot())
		b.WriteString("`\n- `brain/persona.local.md`→`")
		b.WriteString(w.PersonaLocalPath())
		b.WriteString("`\n")
	}
	if out != "" {
		b.WriteString("- output_cwd：`")
		b.WriteString(out)
		b.WriteString("`\n")
	}
	env := ActiveRuntimeEnv()
	if env != nil && env.Tools == (HostTools{}) {
		env.ProbeTools()
	}
	if env != nil {
		b.WriteString(fmt.Sprintf("- host/command：`%s`/`%s`  shell：`%s`\n",
			env.HostPlatform(), env.CommandPlatform(), env.Shell))
		b.WriteString(env.ToolsAvailabilityBlock())
		b.WriteString(env.runCommandHints())
	}
	b.WriteString("工具见 **tools[]**（含 run_command）。\n")
	return b.String()
}

func terminalPathsSystemBlockFull() string {
	out := OutputCwd()
	var b strings.Builder
	b.WriteString(TerminalPathsSystemPrefix)
	b.WriteString("\n")
	b.WriteString("三根：① CATA_HOME=`")
	b.WriteString(CataHome())
	b.WriteString("`（引导+home 记忆）② 项目 `.cata`（persona/modes/skills）③ 产出区 output_cwd（代码/命令）。\n")
	// full 档已注入 constraints：此处只给本轮绝对路径与环境，不复述静态路由。
	if w := Active(); w != nil {
		b.WriteString("- home：`")
		b.WriteString(w.Dir())
		b.WriteString("`\n- 项目 `.cata`：`")
		b.WriteString(w.ProjectCataRoot())
		b.WriteString("`\n- focus_path：`")
		b.WriteString(w.RootPath)
		b.WriteString("`（≠产出区）\n")
		b.WriteString("- `brain/persona.local.md`→`")
		b.WriteString(w.PersonaLocalPath())
		b.WriteString("`\n- `brain/modes/")
		b.WriteString(w.modeID())
		b.WriteString("/persona.md`→`")
		b.WriteString(w.PersonaPath())
		b.WriteString("`\n- `brain/memory/…`→`")
		b.WriteString(w.Dir())
		b.WriteString("/memory/…`\n- `brain/skills/<id>/…`→`")
		b.WriteString(filepath.Join(w.ProjectCataRoot(), DirSkills))
		b.WriteString("/<id>/…`\n")
	} else {
		b.WriteString("- 脑子：（未解析）\n")
	}
	if out != "" {
		b.WriteString("- output_cwd：`")
		b.WriteString(out)
		b.WriteString("`\n")
	} else {
		b.WriteString("- output_cwd：（未知）\n")
	}
	env := ActiveRuntimeEnv()
	if env != nil && env.Tools == (HostTools{}) {
		env.ProbeTools()
	}
	if env == nil {
		b.WriteString(SubagentDelegateGuideBlock())
		return b.String()
	}
	b.WriteString(fmt.Sprintf("- host/command：`%s`/`%s` arch：`%s`  shell：`%s`",
		env.HostPlatform(), env.CommandPlatform(), env.Arch, env.Shell))
	if env.ShellPath != "" {
		b.WriteString(fmt.Sprintf("（`%s`）", env.ShellPath))
	}
	b.WriteString("\n")
	if env.ShellSupportsUnixSyntax() {
		b.WriteString("- 语法：Unix/bash\n")
	} else if env.Shell == "powershell" {
		b.WriteString("- 语法：PowerShell\n")
	} else if env.Shell == "cmd" {
		b.WriteString("- 语法：cmd\n")
	}
	if env.IsWSL() && out != "" && len(out) >= 2 && out[1] == ':' {
		b.WriteString(fmt.Sprintf("- WSL 产出区：`%s`\n", WSLPathForOutput(out)))
	}
	b.WriteString(SubagentDelegateGuideBlock())
	b.WriteString("\n")
	b.WriteString(env.ToolsAvailabilityBlock())
	b.WriteString(env.runCommandHints())
	return b.String()
}

func ServerRegisteredToolsBlock() string {
	cfg := config.Config
	var b strings.Builder
	b.WriteString("### Cata 已注册工具（server 配置）\n\n")
	if cfg == nil {
		b.WriteString("- （配置未加载）\n")
		return b.String()
	}
	if cfg.Exec.Enabled {
		b.WriteString("- **run_command**：已启用 — 在产出区执行 shell/argv\n")
	} else {
		b.WriteString("- run_command：**未启用** — 勿调用；只能读写在文件工具范围内完成\n")
	}
	if cfg.WorkspaceFilesEnabled() {
		b.WriteString("- **read_file / search_replace / append_file / create_file / list_files**：已启用\n")
	} else {
		b.WriteString("- 产出区文件工具：**未启用**\n")
	}
	if cfg.MCP.Enabled {
		names := MCPToolNames()
		if len(names) > 0 {
			b.WriteString("- **MCP 工具**（本轮已导出）：")
			b.WriteString(strings.Join(names, ", "))
			b.WriteString("\n")
		} else {
			b.WriteString("- **browser MCP**：已启用（工具列表待连接后生成）\n")
		}
	} else {
		b.WriteString("- browser MCP：**未启用**\n")
	}
	b.WriteString("- **list_modes / delegate_mode / case_artifact / delegate_wait / delegate_task**：已注册")
	if cfg != nil && cfg.Subagent.MaxConcurrent > 0 {
		b.WriteString(fmt.Sprintf("（子 Agent 并行上限 %d）", cfg.Subagent.MaxConcurrent))
	}
	b.WriteString("\n")
	return b.String()
}

func MCPToolNames() []string {
	if MCPToolNamesProvider == nil {
		return nil
	}
	return MCPToolNamesProvider()
}

