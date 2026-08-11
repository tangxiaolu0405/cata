package gateway

import (
	"strings"

	"cata/internal/cata/socketclient"
)

// CataConn 与 cata server 的长连接（复用共享 socket 客户端协议）。
type CataConn = socketclient.Conn

// StreamHandler 处理 cata 流式事件中的交互。
type StreamHandler = socketclient.StreamHandler

// ChatResult 一轮 chat 结果。
type ChatResult = socketclient.ChatResult

// ExecConfirmPrompt cata 请求用户确认执行命令。
type ExecConfirmPrompt = socketclient.ExecConfirmPrompt

// UserChoicePrompt cata 请求用户选择。
type UserChoicePrompt = socketclient.UserChoicePrompt

// ChoiceOption 可选项。
type ChoiceOption = socketclient.ChoiceOption

// NewCataConn 创建 cata socket 连接句柄（兼容旧调用）。
func NewCataConn(socketPath, cwd string) *CataConn {
	return socketclient.NewConn(socketPath, cwd)
}

// Ping 检查 cata server（兼容旧调用）。
func Ping(socketPath string) error {
	return socketclient.Ping(socketPath)
}

// SplitTelegramMessage 按 Telegram 4096 字符限制切分。
func SplitTelegramMessage(text string, max int) []string {
	if max <= 0 {
		max = 4096
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{"(empty response)"}
	}
	if len(text) <= max {
		return []string{text}
	}
	var parts []string
	for len(text) > max {
		cut := max
		if i := strings.LastIndex(text[:max], "\n"); i > max/2 {
			cut = i
		}
		parts = append(parts, strings.TrimSpace(text[:cut]))
		text = strings.TrimSpace(text[cut:])
	}
	if text != "" {
		parts = append(parts, text)
	}
	return parts
}
