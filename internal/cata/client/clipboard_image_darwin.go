//go:build darwin

package client

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// readClipboardImage macOS：用系统自带的 osascript 把剪贴板图片写成 PNG 文件再读回。
// 优先 PNGf（截图/浏览器复制多为 PNG），失败回退 TIFF 并用 sips 转 PNG。
// 临时文件读完即删，不留痕迹。
func readClipboardImage() (clipboardImage, error) {
	if _, err := exec.LookPath("osascript"); err != nil {
		return clipboardImage{}, fmt.Errorf("读取剪贴板需要 osascript")
	}

	if pngPath, err := writeClipboardClass("PNGf", ".png"); err == nil {
		defer os.Remove(pngPath)
		if data, rerr := os.ReadFile(pngPath); rerr == nil && len(data) > 0 {
			return clipboardImage{Data: data, MIME: "image/png"}, nil
		}
	}

	tiffPath, err := writeClipboardClass("TIFF", ".tiff")
	if err != nil {
		return clipboardImage{}, fmt.Errorf("剪贴板里没有图片（可截图后重试，或用 /attach <path>）")
	}
	defer os.Remove(tiffPath)
	if _, err := exec.LookPath("sips"); err != nil {
		return clipboardImage{}, fmt.Errorf("剪贴板只有 TIFF，需要 sips 转 PNG")
	}
	pngPath := tiffPath + ".png"
	defer os.Remove(pngPath)
	if out, err := exec.Command("sips", "-s", "format", "png", tiffPath, "--out", pngPath).CombinedOutput(); err != nil {
		return clipboardImage{}, fmt.Errorf("sips 转换失败: %s", strings.TrimSpace(string(out)))
	}
	data, err := os.ReadFile(pngPath)
	if err != nil || len(data) == 0 {
		return clipboardImage{}, fmt.Errorf("剪贴板图片读取失败")
	}
	return clipboardImage{Data: data, MIME: "image/png"}, nil
}

// writeClipboardClass 把剪贴板里的某种原始类型（PNGf/TIFF）写进临时文件并返回路径。
// 剪贴板不含该类型时返回错误（AppleScript 抛错）；调用方负责删除返回的文件。
func writeClipboardClass(class, suffix string) (string, error) {
	f, err := os.CreateTemp("", "cata-clip-*"+suffix)
	if err != nil {
		return "", err
	}
	path := f.Name()
	_ = f.Close()
	script := fmt.Sprintf("set f to open for access POSIX file %q with write permission\n"+
		"set eof of f to 0\n"+
		"write (the clipboard as «class %s») to f\n"+
		"close access f", path, class)
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("osascript: %s", strings.TrimSpace(string(out)))
	}
	return path, nil
}
