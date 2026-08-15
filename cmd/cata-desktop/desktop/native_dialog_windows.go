//go:build windows

package desktop

import (
	"encoding/base64"
	"os/exec"
	"strings"
	"unicode/utf16"
)

// pickFolder 弹出 Windows 原生目录选择器（FolderBrowserDialog）。
// 返回选中目录的绝对路径；用户取消返回空字符串。
func pickFolder() string {
	script := `
Add-Type -AssemblyName System.Windows.Forms
$f = New-Object System.Windows.Forms.FolderBrowserDialog
$f.Description = "选择工作空间目录"
if ($f.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
	$f.SelectedPath
}`
	// PowerShell 5.1 默认 STA，FolderBrowserDialog 需要 STA；用 -STA 保险。
	// 用 UTF-16LE base64（-EncodedCommand）避免命令行转义地狱。
	enc := base64.StdEncoding.EncodeToString(utf16ToBytes(script))
	cmd := exec.Command("powershell", "-NoProfile", "-STA", "-EncodedCommand", enc)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return ""
	}
	return path
}

// utf16ToBytes 把字符串编码成 UTF-16LE 字节（PowerShell -EncodedCommand 格式）。
func utf16ToBytes(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 0, len(u)*2)
	for _, r := range u {
		b = append(b, byte(r), byte(r>>8))
	}
	return b
}
