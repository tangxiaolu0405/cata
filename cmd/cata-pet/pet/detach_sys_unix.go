//go:build unix

package pet

import "syscall"

func sysProcAttrDetached() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
