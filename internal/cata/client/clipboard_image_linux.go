//go:build linux

package client

import (
	"fmt"
	"os/exec"
	"strings"
)

// readClipboardImage Linux：Wayland 用 wl-paste，X11 用 xclip（都没有则明确报错）。
func readClipboardImage() (clipboardImage, error) {
	var tried []string

	if p, err := exec.LookPath("wl-paste"); err == nil {
		tried = append(tried, "wl-paste")
		if out, err := exec.Command(p, "-t", "image/png").Output(); err == nil && len(out) > 0 {
			return clipboardImage{Data: out, MIME: "image/png"}, nil
		}
	}
	if p, err := exec.LookPath("xclip"); err == nil {
		tried = append(tried, "xclip")
		if out, err := exec.Command(p, "-selection", "clipboard", "-t", "image/png", "-o").Output(); err == nil && len(out) > 0 {
			return clipboardImage{Data: out, MIME: "image/png"}, nil
		}
	}
	if len(tried) == 0 {
		return clipboardImage{}, fmt.Errorf("剪贴板读取需要 wl-paste（Wayland）或 xclip（X11）：请安装后重试，或用 /attach <path>")
	}
	return clipboardImage{}, fmt.Errorf("剪贴板里没有图片（已尝试 %s；可截图后重试，或用 /attach <path>）", strings.Join(tried, "/"))
}
