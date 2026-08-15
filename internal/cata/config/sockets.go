package config

import (
	"path/filepath"
	"strings"
)

// SocketsDir 每工作空间 agent 的 Unix socket 目录（CATA_HOME/sockets）。
func SocketsDir() string {
	return filepath.Join(CataHome(), "sockets")
}

// SanitizeSocketID 把 ws_id / agent_id 转成安全的文件名片段。
func SanitizeSocketID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	s := b.String()
	if s == "" || s == "." || s == ".." {
		return "default"
	}
	return s
}

// ResolvedAgentSocketPath 某工作空间 agent 的 Unix socket 绝对路径
// （~/.cata/sockets/<ws_id>.sock）。一个 agent = 一个工作空间 = 一个 LLM loop。
func ResolvedAgentSocketPath(wsID string) string {
	return filepath.Join(SocketsDir(), SanitizeSocketID(wsID)+".sock")
}

// SupervisorSocketPath 本机 supervisor（进程生命周期管理）控制 socket。
func SupervisorSocketPath() string {
	return filepath.Join(CataHome(), "supervisor.sock")
}

// LinkConfigPath 隧道/网关注册配置（CATA_HOME/link.json）。
func LinkConfigPath() string {
	return filepath.Join(CataHome(), "link.json")
}

// LogsDir 守护进程日志目录（CATA_HOME/logs）。
func LogsDir() string {
	return filepath.Join(CataHome(), "logs")
}

// AgentLogPath 某 agent 进程的日志文件（CATA_HOME/logs/agent-<id>.log）。
func AgentLogPath(wsID string) string {
	return filepath.Join(LogsDir(), "agent-"+SanitizeSocketID(wsID)+".log")
}

// SupervisorLogPath supervisor 守护进程日志（CATA_HOME/logs/supervisor.log）。
// 守护化（EnsureSupervisorDaemon，stdout/stderr 为 nil）后日志必须落盘，否则全丢。
func SupervisorLogPath() string {
	return filepath.Join(LogsDir(), "supervisor.log")
}

// AgentPIDPath 某 agent 进程的 pid 文件（CATA_HOME/run/agent-<id>.pid）。
func AgentPIDPath(wsID string) string {
	return filepath.Join(CataHome(), "run", "agent-"+SanitizeSocketID(wsID)+".pid")
}
