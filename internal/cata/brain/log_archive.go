package brain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cata/internal/cata/clock"
)

const (
	// FileLLMLog 默认 LLM 请求日志（位于 CATA_HOME）。
	FileLLMLog = "llm.log"
	// FileServerLog cata 守护进程标准 log 输出。
	FileServerLog = "cata-server.log"
)

// ServerLogPath 返回 server 日志绝对路径。
func ServerLogPath() string {
	return filepath.Join(CataHome(), FileServerLog)
}

// LLMLogPath 返回 LLM 日志绝对路径（尊重 LLM_LOG_FILE，相对路径相对 CATA_HOME）。
func LLMLogPath() string {
	if p := strings.TrimSpace(os.Getenv("LLM_LOG_FILE")); p != "" {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(CataHome(), p)
	}
	return filepath.Join(CataHome(), FileLLMLog)
}

// ArchiveSessionLogs 启动时归档已有 llm.log / cata-server.log，便于本次写入新文件。
func ArchiveSessionLogs() error {
	if err := os.MkdirAll(CataHome(), 0755); err != nil {
		return err
	}
	var first error
	if err := archiveLogFileIfExists(ServerLogPath()); err != nil && first == nil {
		first = err
	}
	if err := archiveLogFileIfExists(LLMLogPath()); err != nil && first == nil {
		first = err
	}
	return first
}

// archiveLogFileIfExists 若文件存在则重命名为 name.YYYYMMDD-HHMMSS-RRR.ext。
func archiveLogFileIfExists(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("log path is a directory: %s", path)
	}
	dest, err := archivedLogPath(path)
	if err != nil {
		return err
	}
	return os.Rename(path, dest)
}

func archivedLogPath(path string) (string, error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	ts := clock.Format("20060102-150405")
	n := int(clock.Now().UnixNano() % 1000)
	return filepath.Join(dir, fmt.Sprintf("%s.%s-%03d%s", name, ts, n, ext)), nil
}
