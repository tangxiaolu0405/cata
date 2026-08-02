package brain

import (
	"fmt"
	"strings"
	"sync"
)

// RuntimeEnv 描述产出区所在机器与终端（由 cata chat 每轮上报，注入 LLM）。
type RuntimeEnv struct {
	// OS 喂给 LLM 的命令体系：linux | darwin | windows（WSL 会话内为 linux）
	OS string `json:"os"`
	// HostOS 实际 cata 二进制 GOOS（windows | linux | darwin），仅供调试与平台说明
	HostOS    string `json:"host_os,omitempty"`
	Arch      string `json:"arch"`
	Shell     string `json:"shell"` // bash | powershell | cmd | zsh | ...
	ShellPath string `json:"shell_path,omitempty"`
	Terminal  string `json:"terminal,omitempty"`
	// Tools 客户端 PATH 探测（python/node 等）；由 ProbeTools 填充
	Tools HostTools `json:"tools,omitempty"`
}

// HostTools PATH 中探测到的可执行文件（空字符串表示未找到）。
type HostTools struct {
	Python  string `json:"python,omitempty"`
	Python3 string `json:"python3,omitempty"`
	Node    string `json:"node,omitempty"`
	NPM     string `json:"npm,omitempty"`
	NPX     string `json:"npx,omitempty"`
	Go      string `json:"go,omitempty"`
	Git     string `json:"git,omitempty"`
	Bash    string `json:"bash,omitempty"`
	Pwsh    string `json:"pwsh,omitempty"`
}

var (
	runtimeMu     sync.RWMutex
	activeRuntime *RuntimeEnv
)

// SetRuntimeEnv 设置当前 chat 请求的运行环境。
func SetRuntimeEnv(env *RuntimeEnv) {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	if env == nil {
		activeRuntime = nil
		return
	}
	c := *env
	activeRuntime = &c
}

// ActiveRuntimeEnv 返回当前会话运行环境。
func ActiveRuntimeEnv() *RuntimeEnv {
	runtimeMu.RLock()
	defer runtimeMu.RUnlock()
	if activeRuntime == nil {
		e := DetectRuntimeEnvFromProcess()
		return &e
	}
	out := *activeRuntime
	return &out
}

func (e *RuntimeEnv) runCommandHints() string {
	out := OutputCwd()
	var b strings.Builder

	switch {
	case e.IsWSL():
		b.WriteString("- WSL bash（禁 PowerShell/cmd）。")
		if e.HostOS == "windows" {
			b.WriteString(" argv 例 `[\"wsl.exe\",\"-e\",\"bash\",\"-lc\",\"…\"]`；禁在 `-lc` 里塞多行/长内联，先 `create_file` 再执行；只用 PATH 已有运行时。\n")
		} else {
			b.WriteString(" argv 例 `[\"bash\",\"-lc\",\"…\"]`。\n")
		}
		if out != "" && len(out) >= 2 && out[1] == ':' {
			b.WriteString("- WSL 路径：`")
			b.WriteString(WSLPathForOutput(out))
			b.WriteString("`\n")
		}
	case e.IsGitBash():
		b.WriteString("- Git Bash：bash 语法。argv 例 `[\"bash\",\"-lc\",\"…\"]`。\n")
	case e.Shell == "powershell":
		b.WriteString("- PowerShell 语法；勿用 bash。argv 例 `[\"powershell\",\"-NoProfile\",\"-Command\",\"…\"]`。\n")
	case e.Shell == "cmd":
		b.WriteString("- cmd：`[\"cmd.exe\",\"/c\",\"…\"]`；`mkdir` 非 `-p`。\n")
	default:
		if e.OS == "windows" {
			b.WriteString("- Windows：优先 cmd/PowerShell，勿混 bash。\n")
		} else {
			b.WriteString("- Unix：argv 例 `[\"bash\",\"-lc\",\"…\"]`。\n")
		}
	}
	return b.String()
}

// ShellLineToArgv 将模型给出的一行 shell 命令转为 argv（与当前 RuntimeEnv 一致）。
func ShellLineToArgv(line string) []string {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	e := ActiveRuntimeEnv()
	if e == nil {
		return []string{"cmd.exe", "/c", line}
	}
	switch {
	case e.IsWSL() && e.HostOS == "windows":
		// Windows 二进制在 WSL 会话内：经 wsl.exe 执行 bash
		return []string{"wsl.exe", "-e", "bash", "-lc", line}
	case e.IsWSL() || (e.OS == "linux" && e.Shell == "bash"):
		return []string{"bash", "-lc", line}
	case e.IsGitBash():
		bash := e.ShellPath
		if bash == "" {
			bash = "bash"
		}
		return []string{bash, "-lc", line}
	case e.Shell == "powershell":
		ps := e.ShellPath
		if ps == "" {
			ps = "powershell"
		}
		return []string{ps, "-NoProfile", "-Command", line}
	case e.OS == "windows":
		return []string{"cmd.exe", "/c", line}
	default:
		sh := e.ShellPath
		if sh == "" {
			sh = "/bin/sh"
		}
		return []string{sh, "-lc", line}
	}
}

// RunCommandToolDescription 根据运行环境生成 run_command 工具说明。
func RunCommandToolDescription() string {
	e := ActiveRuntimeEnv()
	verb := "cmd.exe /c"
	if e.IsWSL() || e.Shell == "bash" && e.OS == "linux" {
		verb = "bash -lc"
	} else if e.IsGitBash() || e.Shell == "bash" {
		verb = "bash -lc"
	} else if e.Shell == "powershell" {
		verb = "powershell -Command"
	}
	return fmt.Sprintf(
		"Run in output cwd (NOT ~/.cata). LLM-facing os=%s host_os=%s shell=%s terminal=%s. "+
			"Use API tool_calls argv[]; typical wrapper: %s. Blacklist hits need confirm. %s",
		e.OS, e.HostOS, e.Shell, e.Terminal, verb,
		strings.ReplaceAll(e.runCommandHints(), "\n", " "),
	)
}

// LogBinding 记录脑子与产出区绑定。
func LogBinding() string {
	w := Active()
	env := ActiveRuntimeEnv()
	envS := ""
	if env != nil {
		envS = fmt.Sprintf(" llm_os=%s shell=%s term=%s", env.OS, env.Shell, env.Terminal)
	}
	if w == nil {
		return fmt.Sprintf("brain_home=%s output_cwd=%s%s", CataHome(), OutputCwd(), envS)
	}
	return fmt.Sprintf("brain_id=%s brain_home=%s project_cata=%s focus_path=%s output_cwd=%s%s",
		w.ID, w.Dir(), w.ProjectCataRoot(), w.RootPath, OutputCwd(), envS)
}
