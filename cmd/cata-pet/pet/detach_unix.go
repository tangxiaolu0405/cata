//go:build !windows

package pet

import "os/exec"

func detachCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = sysProcAttrDetached()
}
