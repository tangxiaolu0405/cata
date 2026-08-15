//go:build windows

package pet

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	user32              = syscall.NewLazyDLL("user32.dll")
	procGetForeground   = user32.NewProc("GetForegroundWindow")
	procGetWindowLong   = user32.NewProc("GetWindowLongW")
	procSetWindowLong   = user32.NewProc("SetWindowLongW")
	procSetLayeredAttr  = user32.NewProc("SetLayeredWindowAttributes")
	procEnumWindows     = user32.NewProc("EnumWindows")
	procIsWindowVisible = user32.NewProc("IsWindowVisible")

	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procGetCurrentProcessID = kernel32.NewProc("GetCurrentProcessId")

	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
)

const (
	gwlExStyle      = uintptr(0xFFFFFFEC) // -20
	wsExLayered     = 0x00080000
	wsExTransparent = 0x00000020
	lwaAlpha        = 0x2
)

type winClickThrough struct {
	hwnd uintptr
}

func newClickThrough() ClickThroughController {
	return &winClickThrough{}
}

// ownWindow 枚举本进程的可见顶层窗口，返回其 HWND。
// 不再用 GetForegroundWindow（pet 非前台时会拿到其它应用的窗口并改错样式）。
func (w *winClickThrough) ownWindow() uintptr {
	if w.hwnd != 0 {
		return w.hwnd
	}
	pid, _, _ := procGetCurrentProcessID.Call()
	var found uintptr
	cb := syscall.NewCallback(func(hwnd syscall.Handle, lparam uintptr) uintptr {
		if vis, _, _ := procIsWindowVisible.Call(uintptr(hwnd)); vis == 0 {
			return 1 // continue
		}
		var wpid uintptr
		procGetWindowThreadProcessId.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&wpid)))
		if wpid == pid {
			found = uintptr(hwnd)
			return 0 // stop
		}
		return 1 // continue
	})
	procEnumWindows.Call(cb, 0)
	w.hwnd = found
	return found
}

func (w *winClickThrough) SetIgnoreMouse(ignore bool) error {
	hwnd := w.ownWindow()
	if hwnd == 0 {
		return fmt.Errorf("no window handle")
	}
	style, _, _ := procGetWindowLong.Call(hwnd, gwlExStyle)
	if ignore {
		style |= wsExLayered | wsExTransparent
	} else {
		style |= wsExLayered
		style &^= wsExTransparent
	}
	procSetWindowLong.Call(hwnd, gwlExStyle, style)
	procSetLayeredAttr.Call(hwnd, 0, 255, lwaAlpha)
	return nil
}

func (w *winClickThrough) StartMouseWatch() {}

func platformScreenToContent(sx, sy float64) (float64, float64, bool) {
	_, _ = sx, sy
	return 0, 0, false
}
