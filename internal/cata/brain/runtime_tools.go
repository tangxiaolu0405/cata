package brain

import (
	"fmt"
	"os/exec"
	"strings"
)

// ProbeTools 探测 PATH 中的常用运行时（由客户端每轮 chat 上报前调用）。
func (e *RuntimeEnv) ProbeTools() {
	if e == nil {
		return
	}
	e.Tools = probeHostTools()
}

func probeHostTools() HostTools {
	var t HostTools
	if p, ok := lookExe("python3", "python3.exe"); ok {
		t.Python3 = p
	}
	if p, ok := lookExe("python", "python.exe"); ok {
		t.Python = p
	}
	if t.Python == "" && t.Python3 == "" {
		if p, ok := lookExe("py"); ok {
			t.Python = p
		}
	}
	if p, ok := lookExe("node", "node.exe"); ok {
		t.Node = p
	}
	if p, ok := lookExe("npm", "npm.cmd", "npm.exe"); ok {
		t.NPM = p
	}
	if p, ok := lookExe("npx", "npx.cmd", "npx.exe"); ok {
		t.NPX = p
	}
	if p, ok := lookExe("go", "go.exe"); ok {
		t.Go = p
	}
	if p, ok := lookExe("git", "git.exe"); ok {
		t.Git = p
	}
	if p, ok := lookExe("bash", "bash.exe"); ok {
		t.Bash = p
	}
	if p, ok := lookExe("pwsh", "pwsh.exe", "powershell", "powershell.exe"); ok {
		t.Pwsh = p
	}
	return t
}

func lookExe(names ...string) (string, bool) {
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p, true
		}
	}
	return "", false
}

func (t HostTools) hasPython() bool {
	return t.Python3 != "" || t.Python != ""
}

// CompactBadges 侧栏用短标记，如 `py✓ nd✓ go· git✓`。
func (t HostTools) CompactBadges() string {
	parts := []string{
		toolBadge("py", t.hasPython()),
		toolBadge("nd", t.Node != ""),
		toolBadge("npm", t.NPM != ""),
		toolBadge("go", t.Go != ""),
		toolBadge("git", t.Git != ""),
	}
	return strings.Join(parts, " ")
}

func toolBadge(name string, ok bool) string {
	if ok {
		return name + "✓"
	}
	return name + "·"
}

// CompactEnvLine 侧栏平台 + shell 一行。
func (e *RuntimeEnv) CompactEnvLine() string {
	if e == nil {
		return "env ?"
	}
	plat := e.HostPlatform()
	if cp := e.CommandPlatform(); cp != "" && cp != plat {
		plat = plat + "→" + cp
	}
	sh := e.Shell
	if sh == "" {
		sh = "?"
	}
	if e.ShellSupportsUnixSyntax() {
		return plat + " " + sh + " bash-syntax"
	}
	return plat + " " + sh
}

func (t HostTools) pythonLine() string {
	if t.Python3 != "" {
		if t.Python != "" && t.Python != t.Python3 {
			return fmt.Sprintf("python: yes (`python3` → `%s`; `python` → `%s`)", t.Python3, t.Python)
		}
		return fmt.Sprintf("python: yes (`python3` → `%s`)", t.Python3)
	}
	if t.Python != "" {
		return fmt.Sprintf("python: yes (`python` → `%s`)", t.Python)
	}
	return "python: **not in PATH** — do not assume; use run_command to verify or ask user"
}

func (t HostTools) toolLine(name string, path string) string {
	if path != "" {
		return fmt.Sprintf("%s: yes (`%s` → `%s`)", name, name, path)
	}
	return fmt.Sprintf("%s: **not in PATH**", name)
}

// ToolsAvailabilityBlock 本机 PATH 工具探测（注入 LLM system）。
func (e *RuntimeEnv) ToolsAvailabilityBlock() string {
	if e == nil {
		return ""
	}
	t := e.Tools
	var b strings.Builder
	b.WriteString("### PATH 中的运行时（客户端探测）\n\n")
	b.WriteString(t.pythonLine())
	b.WriteString("\n")
	b.WriteString(t.toolLine("node", t.Node))
	b.WriteString("\n")
	b.WriteString(t.toolLine("npm", t.NPM))
	b.WriteString("\n")
	b.WriteString(t.toolLine("npx", t.NPX))
	b.WriteString("\n")
	b.WriteString(t.toolLine("go", t.Go))
	b.WriteString("\n")
	b.WriteString(t.toolLine("git", t.Git))
	b.WriteString("\n")
	if t.Bash != "" {
		b.WriteString(fmt.Sprintf("bash: yes (`%s`)\n", t.Bash))
	}
	if t.Pwsh != "" {
		b.WriteString(fmt.Sprintf("powershell: yes (`%s`)\n", t.Pwsh))
	}
	b.WriteString("\n未列出的工具同样可能不在 PATH；需要时先用 `run_command` 探测（如 `command -v <tool>` / `which <tool>`），**勿假设**某语言或包管理器可用。\n")
	return b.String()
}

// ShellSyntaxLabel 侧栏 / status 用的语法说明。
func (e *RuntimeEnv) ShellSyntaxLabel() string {
	if e == nil {
		return "unknown"
	}
	if e.ShellSupportsUnixSyntax() {
		return "unix / bash"
	}
	switch e.Shell {
	case "powershell":
		return "PowerShell"
	case "cmd":
		return "cmd"
	default:
		if e.Shell != "" {
			return e.Shell
		}
		return "unknown"
	}
}

func (t HostTools) bestPython() string {
	if t.Python3 != "" {
		return t.Python3
	}
	return t.Python
}

// SidebarToolLines PATH 探测明细（侧栏，可换行）。
func (t HostTools) SidebarToolLines() []string {
	return []string{
		sidebarToolLine("python", t.bestPython()),
		sidebarToolLine("node", t.Node),
		sidebarToolLine("npm", t.NPM),
		sidebarToolLine("npx", t.NPX),
		sidebarToolLine("go", t.Go),
		sidebarToolLine("git", t.Git),
	}
}

func sidebarToolLine(name, path string) string {
	if path != "" {
		return name + "  ✓  " + path
	}
	return name + "  ·  不在 PATH"
}

// HostPlatform 实际机器平台：windows | linux | darwin。
func (e *RuntimeEnv) HostPlatform() string {
	if e == nil || e.HostOS == "" {
		return "unknown"
	}
	return e.HostOS
}

// CommandPlatform 生成 shell 命令时应遵循的平台：linux | darwin | windows。
func (e *RuntimeEnv) CommandPlatform() string {
	if e == nil || e.OS == "" {
		return "unknown"
	}
	return e.OS
}

// ShellSupportsUnixSyntax 当前 shell 是否适合 bash 风格命令。
func (e *RuntimeEnv) ShellSupportsUnixSyntax() bool {
	if e == nil {
		return false
	}
	switch {
	case e.IsWSL(), e.IsGitBash():
		return true
	case e.OS == "linux", e.OS == "darwin":
		return e.Shell == "bash" || e.Shell == "zsh" || e.Shell == "sh" || strings.Contains(e.Shell, "bash")
	default:
		return e.Shell == "bash"
	}
}
