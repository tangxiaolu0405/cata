package brain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cata/internal/cata/clock"
)

const (
	// FileLLMLog 默认 LLM 请求日志（位于 CATA_HOME；多产出区并行时按产出区分文件到 DirLLMLogs）。
	FileLLMLog = "llm.log"
	// FileServerLog cata 守护进程标准 log 输出。
	FileServerLog = "cata-server.log"
	// DirLLMLogs 按产出区拆分的 LLM 请求日志目录。
	DirLLMLogs = "llm"
	// DirLocks ArchiveSessionLogs 等跨进程互斥锁目录。
	DirLocks = "locks"
)

// ServerLogPath 返回 server 日志绝对路径。
func ServerLogPath() string {
	return filepath.Join(CataHome(), FileServerLog)
}

// LLMLogPath 返回当前（全局产出区）LLM 日志绝对路径。
func LLMLogPath() string {
	return LLMLogPathFor(OutputCwd())
}

// LLMLogPathFor 返回指定产出区对应的 LLM 日志绝对路径。
// 显式设置 LLM_LOG_FILE 时保持单文件（兼容旧行为）；否则按产出区拆分到 ~/.cata/llm/<sanitized>.log，
// 多 cata 并行时各 chat 的请求日志不互相串写。
func LLMLogPathFor(outCwd string) string {
	if p := strings.TrimSpace(os.Getenv("LLM_LOG_FILE")); p != "" {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(CataHome(), p)
	}
	if out := strings.TrimSpace(outCwd); out != "" {
		return filepath.Join(CataHome(), DirLLMLogs, llmLogFileName(outCwd))
	}
	return filepath.Join(CataHome(), FileLLMLog)
}

// llmLogFileName 将产出区绝对路径转为日志文件名（与 subagent CSV 同一套命名规则，仅扩展名不同）。
func llmLogFileName(outCwd string) string {
	return strings.TrimSuffix(subagentCSVFileName(outCwd), ".csv") + ".log"
}

// archiveLockPath 跨进程互斥锁路径（ArchiveSessionLogs 用）。
func archiveLockPath() string {
	return filepath.Join(CataHome(), DirLocks, "log_archive.lock")
}

// ArchiveSessionLogs 启动时归档已有 llm.log / cata-server.log / 各产出区 llm/*.log，
// 便于本次写入新文件。用跨进程锁避免多个 server 实例同时归档互相踩踏。
func ArchiveSessionLogs() error {
	if err := os.MkdirAll(CataHome(), 0755); err != nil {
		return err
	}
	if err := acquireArchiveLock(); err != nil {
		// 拿不到锁说明别的实例正在归档：等待其完成即可，不重复归档。
		return waitArchiveLockRelease()
	}
	defer releaseArchiveLock()

	var first error
	if err := archiveLogFileIfExists(ServerLogPath()); err != nil && first == nil {
		first = err
	}
	if err := archiveLogFileIfExists(LLMLogPath()); err != nil && first == nil {
		first = err
	}
	llmDir := filepath.Join(CataHome(), DirLLMLogs)
	if matches, err := filepath.Glob(filepath.Join(llmDir, "*.log")); err == nil {
		for _, p := range matches {
			if err := archiveLogFileIfExists(p); err != nil && first == nil {
				first = err
			}
		}
	}
	return first
}

func acquireArchiveLock() error {
	dir := filepath.Dir(archiveLockPath())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	for i := 0; i < 30; i++ {
		f, err := os.OpenFile(archiveLockPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			_, _ = f.WriteString(clock.RFC3339() + "\n")
			_ = f.Close()
			return nil
		}
		if !os.IsExist(err) {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("archive lock held by another process: %s", archiveLockPath())
}

func waitArchiveLockRelease() error {
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(archiveLockPath()); os.IsNotExist(err) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("archive lock still held: %s", archiveLockPath())
}

func releaseArchiveLock() {
	_ = os.Remove(archiveLockPath())
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
