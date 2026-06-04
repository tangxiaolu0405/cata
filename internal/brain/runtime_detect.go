package brain

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// DetectRuntimeEnvFromProcess 从当前进程环境检测终端类型（客户端上报 / server 回退）。
// Windows 宿主优先级：WSL 环境 > Git Bash > PowerShell > cmd。
func DetectRuntimeEnvFromProcess() RuntimeEnv {
	e := RuntimeEnv{
		HostOS: runtime.GOOS,
		Arch:   runtime.GOARCH,
	}
	switch runtime.GOOS {
	case "windows":
		detectWindowsHost(&e)
	case "linux":
		detectLinuxHost(&e)
	default:
		detectUnixHost(&e)
	}
	if e.OS == "" {
		e.OS = runtime.GOOS
	}
	e.ProbeTools()
	return e
}

// detectWindowsHost 在 GOOS=windows 上检测终端。
// 已在 WSL 会话内 → 一律按 Linux/bash 喂给 LLM；否则 Git Bash（已安装/当前会话）优先于 PowerShell。
func detectWindowsHost(e *RuntimeEnv) {
	if detectWSLInterop(e) {
		return
	}
	if detectGitBashSession(e) {
		return
	}
	if preferInstalledGitBash(e) {
		return
	}
	detectWindowsPowerShellOrCmd(e)
}

func detectWSLInterop(e *RuntimeEnv) bool {
	distro := strings.TrimSpace(os.Getenv("WSL_DISTRO_NAME"))
	if distro == "" && !strings.Contains(os.Getenv("WSLENV"), ":") {
		return false
	}
	if distro == "" {
		distro = "wsl"
	}
	e.OS = "linux" // 喂给 LLM：按 Linux/bash，不要 PowerShell
	e.Shell = "bash"
	e.ShellPath = strings.TrimSpace(os.Getenv("SHELL"))
	if e.ShellPath == "" {
		e.ShellPath = "bash"
	}
	e.Terminal = "wsl:" + distro
	return true
}

func detectGitBashSession(e *RuntimeEnv) bool {
	if msys := strings.TrimSpace(os.Getenv("MSYSTEM")); msys != "" {
		e.OS = "windows"
		e.Shell = "bash"
		e.ShellPath = strings.TrimSpace(os.Getenv("SHELL"))
		if e.ShellPath == "" {
			e.ShellPath = findGitBashExe()
		}
		e.Terminal = "git-bash:" + msys
		return true
	}
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	low := strings.ToLower(shell)
	if strings.Contains(low, "bash") && (strings.Contains(low, `git\`) || strings.Contains(low, `/git/`) || strings.Contains(low, "mingw")) {
		e.OS = "windows"
		e.Shell = "bash"
		e.ShellPath = shell
		e.Terminal = "git-bash"
		return true
	}
	if bash := findGitBashExe(); bash != "" {
		term := strings.ToLower(os.Getenv("TERM"))
		if strings.Contains(term, "xterm") || strings.Contains(term, "screen") {
			e.OS = "windows"
			e.Shell = "bash"
			e.ShellPath = bash
			e.Terminal = "git-bash"
			return true
		}
	}
	return false
}

// preferInstalledGitBash 本机已装 Git for Windows 时，优先于默认 PowerShell（用户未在 WSL 会话内）。
func preferInstalledGitBash(e *RuntimeEnv) bool {
	bash := findGitBashExe()
	if bash == "" || isPowerShellSession() {
		return false
	}
	e.OS = "windows"
	e.Shell = "bash"
	e.ShellPath = bash
	e.Terminal = "git-bash:installed"
	return true
}

func isPowerShellShell(comspec string) bool {
	low := strings.ToLower(comspec)
	return strings.Contains(low, "powershell") || strings.Contains(low, "pwsh")
}

func detectWindowsTerminal() string {
	if os.Getenv("WT_SESSION") != "" {
		return "Windows Terminal"
	}
	if v := strings.TrimSpace(os.Getenv("TERM_PROGRAM")); v != "" {
		return v
	}
	if os.Getenv("VSCODE_GIT_IPC_HANDLE") != "" || os.Getenv("CURSOR_TRACE_ID") != "" {
		return "vscode/cursor"
	}
	return "console"
}

func isPowerShellSession() bool {
	if isPowerShellShell(os.Getenv("COMSPEC")) {
		return true
	}
	if strings.Contains(strings.ToLower(os.Getenv("SHELL")), "powershell") {
		return true
	}
	return false
}

func detectWindowsPowerShellOrCmd(e *RuntimeEnv) {
	e.OS = "windows"
	e.ShellPath = os.Getenv("COMSPEC")
	if isPowerShellShell(e.ShellPath) || isPowerShellSession() {
		e.Shell = "powershell"
		if pwsh, err := exec.LookPath("pwsh.exe"); err == nil {
			e.ShellPath = pwsh
		} else if ps, err := exec.LookPath("powershell.exe"); err == nil {
			e.ShellPath = ps
		}
	} else {
		e.Shell = "cmd"
		if e.ShellPath == "" {
			e.ShellPath = `C:\Windows\System32\cmd.exe`
		}
	}
	e.Terminal = detectWindowsTerminal()
}

func detectLinuxHost(e *RuntimeEnv) {
	e.OS = "linux"
	e.ShellPath = strings.TrimSpace(os.Getenv("SHELL"))
	e.Shell = strings.TrimPrefix(filepath.Base(e.ShellPath), "/")
	if e.Shell == "" || e.Shell == "sh" {
		e.Shell = "bash"
	}
	if inWSLLinux() {
		distro := strings.TrimSpace(os.Getenv("WSL_DISTRO_NAME"))
		if distro == "" {
			distro = "linux"
		}
		e.Terminal = "wsl:" + distro
	} else {
		e.Terminal = strings.TrimSpace(os.Getenv("TERM_PROGRAM"))
	}
}

func detectUnixHost(e *RuntimeEnv) {
	e.OS = runtime.GOOS
	e.ShellPath = os.Getenv("SHELL")
	e.Shell = filepath.Base(e.ShellPath)
	e.Terminal = strings.TrimSpace(os.Getenv("TERM_PROGRAM"))
}

func inWSLLinux() bool {
	b, err := os.ReadFile("/proc/version")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}

func findGitBashExe() string {
	if p, err := exec.LookPath("bash.exe"); err == nil {
		low := strings.ToLower(p)
		if strings.Contains(low, `\git\`) || strings.Contains(low, "/git/") {
			return p
		}
	}
	for _, p := range []string{
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files\Git\usr\bin\bash.exe`,
		`C:\Program Files (x86)\Git\bin\bash.exe`,
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// IsWSL 当前会话是否应按 WSL/Linux bash 生成命令（含 Windows 上跑 cata.exe 的 WSL Interop）。
func (e *RuntimeEnv) IsWSL() bool {
	if e == nil {
		return false
	}
	return e.OS == "linux" || strings.HasPrefix(e.Terminal, "wsl:")
}

// IsGitBash 是否为 Windows 上的 Git Bash。
func (e *RuntimeEnv) IsGitBash() bool {
	if e == nil {
		return false
	}
	return e.Shell == "bash" && strings.HasPrefix(e.Terminal, "git-bash")
}

// WSLPathForOutput 将 Windows 产出区路径转为 WSL 内路径（供 LLM 写 bash 命令）。
func WSLPathForOutput(windowsPath string) string {
	p := strings.TrimSpace(windowsPath)
	if len(p) < 2 || p[1] != ':' {
		return p
	}
	drive := strings.ToLower(string(p[0]))
	rest := filepath.ToSlash(p[2:])
	rest = strings.TrimPrefix(rest, "/")
	return "/mnt/" + drive + "/" + rest
}
