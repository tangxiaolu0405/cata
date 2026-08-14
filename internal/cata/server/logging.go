package server

import (
	"io"
	"log"
	"os"
	"path/filepath"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
)

// SetupProcessLogging 将标准 log 写入新 cata-server.log（启动前已归档旧文件）。
func SetupProcessLogging(managed bool) {
	if !managed {
		return
	}
	path := brain.ServerLogPath()
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(f))
}

// SetupAgentLogging 将标准 log 写入 ~/.cata/logs/agent-<id>.log（agent 模式；keep-alive 常驻用）。
func SetupAgentLogging(agentID string) {
	path := config.AgentLogPath(agentID)
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(f))
}
