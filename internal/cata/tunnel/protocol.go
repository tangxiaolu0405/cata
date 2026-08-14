// Package tunnel 定义 cata-tunnel.v1 协议：agent 进程（worker）持有到网关的 WSS 连接，
// 将多条「逻辑 socket 连接」（stream）多路复用到本机 per-ws Unix socket 上。
// 底层 chat 协议（NDJSON 行）零改动——隧道只是逐字节透传。
package tunnel

import "encoding/base64"

const (
	// ProtocolName 协议标识（hello 帧携带）。
	ProtocolName = "cata-tunnel.v1"
	// Version 协议版本。
	Version = 1
	// MaxFrameBytes 单帧上限（8 MiB）。超过即断开，防止异常大消息撑爆内存。
	MaxFrameBytes = 8 << 20
)

// FrameType 帧类型。
const (
	FrameHello  = "hello"  // worker → gateway：注册 agent（agent_id/name/root_path/protocol）
	FrameOpen   = "open"   // gateway → worker：打开一条新 stream（逻辑 socket 连接）
	FrameOpened = "opened" // worker → gateway：stream 已建立（本地 per-ws socket 已拨通）
	FrameLine   = "line"   // 双向：stream 上的原始字节（base64，不含行尾约定，原样透传）
	FrameClose  = "close"  // 双向：关闭 stream
	FrameError  = "error"  // worker → gateway：stream 错误
	FramePing   = "ping"   // 双向：保活
	FramePong   = "pong"   // 双向：保活应答
	FrameDetach = "detach" // 预留
)

// Frame 隧道帧（JSON over WebSocket text message）。
type Frame struct {
	Type     string `json:"type"`
	AgentID  string `json:"agent_id,omitempty"`
	Name     string `json:"name,omitempty"`
	RootPath string `json:"root_path,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Version  int    `json:"version,omitempty"`
	Stream   uint64 `json:"stream,omitempty"`
	Data     string `json:"data,omitempty"` // base64（line 帧）
	Message  string `json:"message,omitempty"`
}

// EncodeData 字节 → base64 字符串。
func EncodeData(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// DecodeData base64 字符串 → 字节。
func DecodeData(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }
