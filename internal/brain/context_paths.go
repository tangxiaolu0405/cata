package brain

import (
	"fmt"
	"strings"
	"sync"
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
	b.WriteString("\n## 运行环境（run_command 必遵）\n\n")
	b.WriteString(fmt.Sprintf("- llm_os（命令语法）：`%s`  host_os（二进制）：`%s`  arch：`%s`\n",
		env.OS, env.HostOS, env.Arch))
	b.WriteString(fmt.Sprintf("- shell：`%s`", env.Shell))
	if env.ShellPath != "" {
		b.WriteString(fmt.Sprintf("（`%s`）", env.ShellPath))
	}
	b.WriteString("\n")
	if env.Terminal != "" {
		b.WriteString(fmt.Sprintf("- terminal：`%s`\n", env.Terminal))
	}
	if env.IsWSL() && out != "" && len(out) >= 2 && out[1] == ':' {
		b.WriteString(fmt.Sprintf("- 产出区 WSL 路径：`%s`\n", WSLPathForOutput(out)))
	}
	b.WriteString("\n")
	b.WriteString(env.runCommandHints())
	b.WriteString("\n改产出区用默认路径；改脑子文档用 `brain/modes/...` 或 `global/constraints.md`；也可读 system 已注入节选。\n")
	b.WriteString("改文件优先 **read_file** → **search_replace** / **append_file**；跑命令用 **run_command**。禁止只写代码块或 XML 假装已执行。\n")
	return b.String()
}

