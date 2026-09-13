package client

// 剪贴板图片读取（TUI 上传入口之一）。
//
// 终端没有通用的「粘贴图片」协议，所以这里按平台调用系统剪贴板：
//   - macOS：osascript（系统自带；PNGf 优先，TIFF 回退用 sips 转 PNG）
//   - Linux：wl-paste（Wayland）或 xclip（X11）
//   - Windows：PowerShell + System.Windows.Forms.Clipboard
//
// 读到的字节统一走 socket 协议的 **inline 附件**（base64），由 server 侧
// ingestInlineAttachment 校验 MIME/大小后编码为 data:<mime>;base64,… 发给模型
// （DeepSeek 图像理解要求的 base64 内联格式）。不落盘到产出区。

// clipboardImage 剪贴板里读到的图片（目前统一 PNG）。
type clipboardImage struct {
	Data []byte
	MIME string
}
