package server

import (
	"fmt"
	"runtime"
	"strings"

	"cata/internal/cata/execcmd"
)

// windowsCreateProcessCmdLimit Windows CreateProcess 命令行上限（保守值）。
const windowsCreateProcessCmdLimit = 8000

// checkRunCommandArgv 启动前校验；Windows 上过长的 wsl/bash -lc 会静默失败或挂起。
func checkRunCommandArgv(argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("run_command: argv required")
	}
	if runtime.GOOS == "windows" {
		line := execcmd.FormatLine(argv)
		if len(line) > windowsCreateProcessCmdLimit {
			return fmt.Errorf(
				"run_command: command line too long for Windows (%d > %d). Write content with create_file/append_file, then run a short script (e.g. wsl bash /mnt/d/.../write.sh)",
				len(line), windowsCreateProcessCmdLimit,
			)
		}
	}
	if err := checkFragileShellScript(argv); err != nil {
		return err
	}
	return nil
}

// checkFragileShellScript 拦截易在 wsl/bash -lc 下挂死或引号损坏的写法。
func checkFragileShellScript(argv []string) error {
	script, ok := shellLcScript(argv)
	if !ok {
		return nil
	}
	if strings.ContainsAny(script, "\r\n") {
		return fmt.Errorf(
			"run_command: multiline bash -lc script is fragile (hangs/quote-breaks on Windows WSL). " +
				"Use create_file to write a script, then run a one-line command with a runtime that exists in PATH",
		)
	}
	if looksLikeInlineInterpreterCode(script) {
		return fmt.Errorf(
			"run_command: long inline -c/-e code via bash -lc is fragile on Windows WSL. " +
				"Write a script file with create_file, then run a one-line command (only use runtimes listed as available in PATH)",
		)
	}
	if strings.Contains(script, "<<") && len(script) > 1500 {
		return fmt.Errorf(
			"run_command: large bash heredoc via -lc is fragile on Windows; prefer create_file/append_file then run a short one-line command",
		)
	}
	return nil
}

// looksLikeInlineInterpreterCode 识别 python/node/ruby 等经 -c/-e 塞进长代码的写法（不绑定某一语言）。
func looksLikeInlineInterpreterCode(script string) bool {
	lower := strings.ToLower(script)
	markers := []string{
		" -c ", " -c\"", " -c'",
		" -e ", " -e\"", " -e'",
		" -r ", " -r\"", " -r'",
		" --eval ", " --eval=",
	}
	has := false
	for _, m := range markers {
		if strings.Contains(lower, m) {
			has = true
			break
		}
	}
	if !has {
		return false
	}
	return len(script) > 180 || countQuotes(script) >= 4
}

func shellLcScript(argv []string) (string, bool) {
	for i := 0; i < len(argv)-1; i++ {
		flag := strings.ToLower(strings.TrimSpace(argv[i]))
		if flag == "-lc" || flag == "-c" {
			// bash -lc SCRIPT / sh -c SCRIPT
			prev := ""
			if i > 0 {
				prev = strings.ToLower(baseName(argv[i-1]))
			}
			if strings.Contains(prev, "bash") || strings.Contains(prev, "sh") || prev == "wsl" || prev == "wsl.exe" {
				return argv[i+1], true
			}
			// wsl.exe -e bash -lc SCRIPT → prev is bash
		}
	}
	// ["wsl.exe","-e","bash","-lc",script]
	if len(argv) >= 5 {
		joined := strings.ToLower(strings.Join(argv[:len(argv)-1], " "))
		if strings.Contains(joined, "wsl") && strings.Contains(joined, "bash") &&
			(strings.Contains(joined, "-lc") || strings.HasSuffix(joined, "-c")) {
			return argv[len(argv)-1], true
		}
	}
	return "", false
}

func baseName(s string) string {
	s = strings.ReplaceAll(s, "\\", "/")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

func countQuotes(s string) int {
	n := 0
	for _, r := range s {
		if r == '"' || r == '\'' {
			n++
		}
	}
	return n
}
