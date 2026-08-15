//go:build windows

package scheduler

import "os/exec"

// detachCmd Windows 无 setsid；直接分离（隐藏窗口交给服务包装）。
func detachCmd(cmd *exec.Cmd) {
}
