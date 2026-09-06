// Package tunnel 定义 cata-tunnel.v1 协议：agent 进程（worker）持有到网关的 WSS 连接，
// 将多条「逻辑 socket 连接」（stream）多路复用到本机 per-ws Unix socket 上。
// 底层 chat 协议（NDJSON 行）零改动——隧道只是逐字节透传。
package tunnel

import (
	"encoding/base64"
	"time"
)

const (
	// ProtocolName 协议标识（hello 帧携带）。
	ProtocolName = "cata-tunnel.v1"
	// Version 协议版本。
	Version = 1
	// MaxFrameBytes 单帧上限（8 MiB）。超过即断开，防止异常大消息撑爆内存。
	MaxFrameBytes = 8 << 20
	// HeartbeatInterval 网关侧 ping 周期；读 deadline 为 3×该值。
	// 用于检测 NAT 超时 / 静默断网的半开连接，使陈旧注册可自愈。
	HeartbeatInterval = 20 * time.Second
)

// FrameType 帧类型。
const (
	FrameHello    = "hello"     // worker → gateway：注册 agent（agent_id/name/root_path/machine_id/protocol）
	FrameHelloAck = "hello_ack" // gateway → worker：hello 已受理；可携带 agent_token（per-agent 签发）
	FrameOpen     = "open"      // gateway → worker：打开一条新 stream（逻辑 socket 连接）
	FrameOpened   = "opened"    // worker → gateway：stream 已建立（本地 per-ws socket 已拨通）
	FrameLine     = "line"      // 双向：stream 上的原始字节（base64，不含行尾约定，原样透传）
	FrameClose    = "close"     // 双向：关闭 stream
	FrameError    = "error"     // worker → gateway：stream 错误
	FramePing     = "ping"      // 双向：保活
	FramePong     = "pong"      // 双向：保活应答
	FrameRegister = "register"  // gateway → worker：命令本机注册一个新工作空间（相对 workspace_root 的子路径）
	FrameDetach   = "detach"    // 预留
)

// Frame 隧道帧（JSON over WebSocket text message）。
type Frame struct {
	Type     string `json:"type"`
	AgentID  string `json:"agent_id,omitempty"`
	Name     string `json:"name,omitempty"`
	RootPath string `json:"root_path,omitempty"`
	// MachineID 机器标识（hello 帧携带）：gateway 用它把在线 agent 按机器分组，
	// 并向同一机器下发 register 控制帧。worker 侧生成并填充。
	MachineID string `json:"machine_id,omitempty"`
	// MachineToken 逐机器凭证（hello 帧携带）：join 流程签发的本机独立 token。
	// gateway 按 machine_id 查表比对 hash，单机泄露可单独吊销（替代 v1 共享 token）。
	MachineToken string `json:"machine_token,omitempty"`
	// AgentToken per-agent 凭证（hello 帧携带；hello_ack 下发）。首次注册由网关按
	// machine 权威签发并下发，之后每 agent 用独立 token（吊销粒度细化到工作空间）。
	// 空 = 回退 machine token 校验（兼容旧 worker）。
	AgentToken string `json:"agent_token,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
	Version    int    `json:"version,omitempty"`
	Stream     uint64 `json:"stream,omitempty"`
	Data       string `json:"data,omitempty"` // base64（line 帧）
	Message    string `json:"message,omitempty"`
}

// EncodeData 字节 → base64 字符串。
func EncodeData(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// DecodeData base64 字符串 → 字节。
func DecodeData(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }
