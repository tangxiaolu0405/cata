package client

import (
	"os"
	"testing"
)

// TestLiveClipboardImage 真机检查剪贴板读取的平台实现（默认跳过；不修改剪贴板）：
// 剪贴板有图片 → 返回 PNG 字节；没有图片 → 明确报错（不 panic、不卡住）。
func TestLiveClipboardImage(t *testing.T) {
	if os.Getenv("CATA_LIVE_CLIPBOARD_TEST") != "1" {
		t.Skip("set CATA_LIVE_CLIPBOARD_TEST=1 to probe the real system clipboard")
	}
	img, err := readClipboardImage()
	if err != nil {
		t.Logf("no clipboard image (expected on a text clipboard): %v", err)
		return
	}
	if len(img.Data) == 0 || img.MIME != "image/png" {
		t.Fatalf("unexpected clipboard image: mime=%q bytes=%d", img.MIME, len(img.Data))
	}
	t.Logf("clipboard image: %s, %d bytes", img.MIME, len(img.Data))
}
