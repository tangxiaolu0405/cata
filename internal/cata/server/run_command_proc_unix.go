//go:build !windows

package server

import (
	"os/exec"
	"syscall"
)

func killCmdTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
