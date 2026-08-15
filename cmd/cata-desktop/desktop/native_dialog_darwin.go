//go:build darwin

package desktop

import (
	"os/exec"
	"strings"
)

// pickFolder 弹出 macOS 原生目录选择器（NSOpenPanel 风格，osascript choose folder）。
// 返回选中目录的绝对路径；用户取消返回空字符串。
func pickFolder() string {
	// choose folder 返回 alias，POSIX path 转成路径；结果可能带换行。
	out, err := exec.Command("osascript", "-e", `POSIX path of (choose folder)`).Output()
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return ""
	}
	return path
}
