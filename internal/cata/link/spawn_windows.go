//go:build windows

package link

import "os/exec"

func detachCmd(cmd *exec.Cmd) {
	// Windows 无 setsid；直接分离（隐藏窗口交给服务包装）。
}
