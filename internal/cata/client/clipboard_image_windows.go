//go:build windows

package client

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// readClipboardImage Windows：PowerShell 读剪贴板图片并另存 PNG。
// 需要 -STA（剪贴板 API 要求单线程单元）。
func readClipboardImage() (clipboardImage, error) {
	ps, err := exec.LookPath("powershell")
	if err != nil {
		if ps, err = exec.LookPath("pwsh"); err != nil {
			return clipboardImage{}, fmt.Errorf("剪贴板读取需要 powershell")
		}
	}
	f, err := os.CreateTemp("", "cata-clip-*.png")
	if err != nil {
		return clipboardImage{}, err
	}
	path := f.Name()
	_ = f.Close()
	_ = os.Remove(path) // 只要路径：交给 PowerShell 写
	defer os.Remove(path)

	script := fmt.Sprintf(
		`Add-Type -AssemblyName System.Windows.Forms,System.Drawing; `+
			`$img=[System.Windows.Forms.Clipboard]::GetImage(); `+
			`if ($img -eq $null) { exit 3 }; `+
			`$img.Save('%s',[System.Drawing.Imaging.ImageFormat]::Png)`, path)
	if out, err := exec.Command(ps, "-NoProfile", "-STA", "-Command", script).CombinedOutput(); err != nil {
		if strings.Contains(err.Error(), "exit status 3") {
			return clipboardImage{}, fmt.Errorf("剪贴板里没有图片（可截图后重试，或用 /attach <path>）")
		}
		return clipboardImage{}, fmt.Errorf("读取剪贴板失败: %s", strings.TrimSpace(string(out)))
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return clipboardImage{}, fmt.Errorf("剪贴板图片读取失败")
	}
	return clipboardImage{Data: data, MIME: "image/png"}, nil
}
