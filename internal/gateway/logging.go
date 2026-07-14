package gateway

import (
	"io"
	"log"
	"os"
	"path/filepath"

	"cata/internal/cata/brain"
)

const gatewayLogFile = "cata-gateway.log"

// LogPath gateway 日志绝对路径（CATA_HOME/cata-gateway.log）。
func LogPath() string {
	return filepath.Join(brain.CataHome(), gatewayLogFile)
}

// SetupLogging 同时写入 stderr 与 ~/.cata/cata-gateway.log。
func SetupLogging() {
	path := LogPath()
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("cata-gateway: cannot open log file %s: %v (stderr only)", path, err)
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	log.Printf("cata-gateway: logging to %s", path)
}
