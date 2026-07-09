package brain

import (
	"fmt"
	"strings"
	"sync"

	"cata/internal/config"
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
	home := CataHome()
	out := OutputCwd()
	var b strings.Builder
	b.WriteString(TerminalPathsSystemPrefix)
	b.WriteString("\n\n")
	b.WriteString("## 路径约定（必遵）\n\n")
	b.WriteString("- **CATA_HOME `")
	b.WriteString(home)
	b.WriteString("/`**：引导型提示词（boot-assembler、global/constraints、global/behavior）+ 运行时记忆（short-term、long-term、index）；**禁止**把项目交付物写入此处。\n")
	b.WriteString("- **产出区（Output）**：当前工作目录。默认相对路径、`run_command`、构建与交付物在此。\n")
	b.WriteString("- **文件工具路径**：默认=产出区；`brain/…`=项目 `.cata` 或 home 记忆（见下）；`global/…`=`")
	b.WriteString(home)
	b.WriteString("/global/`（引导，非 evolve 写入）。\n")
	b.WriteString("- **项目 `.cata/`**（focus_path 下）：主要内容提示词（persona、modes、skills）；`workspace.yaml` 为门牌。\n\n")
	b.WriteString("## 当前绑定\n\n")
	if w := Active(); w != nil {
		b.WriteString("- 脑子 home 格：`")
		b.WriteString(w.Dir())
		b.WriteString("`\n")
		b.WriteString("- 项目脑子文档：`")
		b.WriteString(w.ProjectCataRoot())
		b.WriteString("`（persona、modes、skills）\n")
		b.WriteString("- 脑子绑定键 focus_path：`")
		b.WriteString(w.RootPath)
		b.WriteString("`（用于选哪一格脑子，≠ 产出区）\n")
		b.WriteString("\n### brain/ 工具路径解析（本轮）\n\n")
		b.WriteString("- `brain/persona.local.md` → `")
		b.WriteString(w.PersonaLocalPath())
		b.WriteString("`\n")
		b.WriteString("- `brain/modes/")
		b.WriteString(w.modeID())
		b.WriteString("/persona.md` → `")
		b.WriteString(w.PersonaPath())
		b.WriteString("`\n")
		b.WriteString("- `brain/memory/…` → `")
		b.WriteString(w.Dir())
		b.WriteString("/memory/…`\n")
	} else {
		b.WriteString("- 脑子分区：（未解析）\n")
	}
	if out != "" {
		b.WriteString("- 产出区 output_cwd：`")
		b.WriteString(out)
		b.WriteString("`\n")
	} else {
		b.WriteString("- 产出区 output_cwd：（未知）\n")
	}
	env := ActiveRuntimeEnv()
	if env != nil && env.Tools == (HostTools{}) {
		env.ProbeTools()
	}
	b.WriteString("\n## 本机环境（run_command / 脚本语法必遵）\n\n")
	b.WriteString(fmt.Sprintf("- **host 平台**（物理/宿主）：`%s`  **command 平台**（命令语法）：`%s`  arch：`%s`\n",
		env.HostPlatform(), env.CommandPlatform(), env.Arch))
	b.WriteString(fmt.Sprintf("- **终端**：`%s`  **shell**：`%s`", env.Terminal, env.Shell))
	if env.ShellPath != "" {
		b.WriteString(fmt.Sprintf("（`%s`）", env.ShellPath))
	}
	b.WriteString("\n")
	if env.ShellSupportsUnixSyntax() {
		b.WriteString("- **shell 语法**：Unix/bash 风格（`mkdir -p`、`ls`、`grep`、heredoc 等）\n")
	} else if env.Shell == "powershell" {
		b.WriteString("- **shell 语法**：PowerShell（勿用 bash 的 `mkdir -p` / heredoc）\n")
	} else if env.Shell == "cmd" {
		b.WriteString("- **shell 语法**：Windows cmd（`mkdir` 非 `-p`、`dir`、`type nul >`）\n")
	} else {
		b.WriteString("- **shell 语法**：按 command 平台选择；勿混用 bash 与 PowerShell\n")
	}
	if env.IsWSL() && out != "" && len(out) >= 2 && out[1] == ':' {
		b.WriteString(fmt.Sprintf("- 产出区 WSL 路径：`%s`\n", WSLPathForOutput(out)))
	}
	b.WriteString("\n")
	b.WriteString(ServerRegisteredToolsBlock())
	b.WriteString("\n")
	b.WriteString(SubagentDelegateGuideBlock())
	b.WriteString("\n")
	b.WriteString(env.ToolsAvailabilityBlock())
	b.WriteString("\n")
	b.WriteString(env.runCommandHints())
	b.WriteString("\n改产出区用默认路径；改**项目内容**用 `brain/modes/...`、`brain/persona.local.md`；改**全机引导**用 `global/constraints.md`（须用户明确同意，evolve 不写）。\n")
	b.WriteString("改文件优先 **read_file** → **search_replace** / **append_file**；跑命令用 **run_command**。禁止只写代码块或 XML 假装已执行。\n")
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
	b.WriteString("- **read_skill / run_skill / ask_user / delegate_task / delegate_wait**：已注册")
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

