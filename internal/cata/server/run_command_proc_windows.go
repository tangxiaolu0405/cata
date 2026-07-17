//go:build windows

package server

import (
	"os/exec"
	"strconv"
	"time"
)

// killCmdTree 终止进程树（WSL 子进程在仅 Kill wsl.exe 时经常残留并拖住 Wait）。
func killCmdTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	// /T = 树杀；忽略错误（进程可能已退出）
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
	// 给一点时间收尸，避免 Wait 永久阻塞
	time.Sleep(50 * time.Millisecond)
	_ = cmd.Process.Kill()
}
