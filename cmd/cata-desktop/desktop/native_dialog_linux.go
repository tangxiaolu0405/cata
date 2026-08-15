//go:build linux

package desktop

import (
	"os/exec"
	"strings"
)

// pickFolder 弹出 Linux 原生目录选择器：优先 zenity，其次 kdialog。
// 返回选中目录的绝对路径；用户取消返回空字符串。
func pickFolder() string {
	if out, ok := runPicker("zenity", "--file-selection", "--directory", "--title=选择工作空间目录"); ok {
		return out
	}
	if out, ok := runPicker("kdialog", "--getexistingdirectory", "选择工作空间目录"); ok {
		return out
	}
	return ""
}

// runPicker 执行选择器命令，成功（退出码 0 且输出非空）返回输出。
func runPicker(name string, args ...string) (string, bool) {
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", false
	}
	return path, true
}
