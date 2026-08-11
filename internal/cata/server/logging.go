package server

import (
	"io"
	"log"
	"os"
	"path/filepath"

	"cata/internal/cata/brain"
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
