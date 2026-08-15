//go:build !windows

package scheduler

import (
	"os/exec"
	"syscall"
)

// detachCmd 让子进程脱离当前会话（setsid），后台守护不被终端退出信号波及。
func detachCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
