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
	b.WriteString("- **脑子（Brain）**：`")
	b.WriteString(home)
	b.WriteString("/`（CATA_HOME）。记忆、persona、short-term、evolution_log 只在脑子目录；**禁止**把用户项目交付物写入脑子。\n")
	b.WriteString("- **产出区（Output）**：当前工作目录。默认相对路径、`run_command`、构建与交付物在此。\n")
	b.WriteString("- **文件工具路径**：默认=产出区；`brain/…`=当前脑子 workspace 格；`global/…`=全机 global（`")
	b.WriteString(home)
	b.WriteString("/global/`）。\n")
	b.WriteString("- 项目内 `.cata/workspace.yaml` 仅是**门牌**（绑定哪一格脑子），不是脑子正文。\n\n")
	b.WriteString("## 当前绑定\n\n")
	if w := Active(); w != nil {
		b.WriteString("- 脑子分区目录：`")
		b.WriteString(w.Dir())
		b.WriteString("`\n")
		b.WriteString("- 脑子绑定键 focus_path：`")
		b.WriteString(w.RootPath)
		b.WriteString("`（用于选哪一格脑子，≠ 产出区）\n")
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
	b.WriteString(env.ToolsAvailabilityBlock())
	b.WriteString("\n")
	b.WriteString(env.runCommandHints())
	b.WriteString("\n改产出区用默认路径；改脑子文档用 `brain/modes/...` 或 `global/constraints.md`；也可读 system 已注入节选。\n")
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
		b.WriteString("- **browser_*（MCP Playwright）**：已启用 — 见 capabilities\n")
	} else {
		b.WriteString("- browser MCP：**未启用**\n")
	}
	b.WriteString("- **run_skill / ask_user**：已注册\n")
	return b.String()
}

