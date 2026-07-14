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
	// bash -lc 内嵌巨型 heredoc 易因引号/换行损坏而阻塞 stdin
	if len(argv) >= 4 {
		joined := strings.ToLower(strings.Join(argv, " "))
		if strings.Contains(joined, "wsl") && strings.Contains(joined, "bash") &&
			strings.Contains(joined, "<<") && len(argv[len(argv)-1]) > 1500 {
			return fmt.Errorf(
				"run_command: large bash heredoc via wsl -lc is fragile on Windows; prefer create_file/append_file then run a short wsl script",
			)
		}
	}
	return nil
}
