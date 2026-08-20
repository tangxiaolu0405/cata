package brain

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"cata/internal/cata/clock"
	"cata/internal/cata/config"
)

const (
	// FileLLMLog 默认 LLM 请求日志（位于 logs/server；多产出区并行时按产出区分文件到 DirLLMLogs）。
	FileLLMLog = "llm.log"
	// FileServerLog cata 守护进程标准 log 输出。
	FileServerLog = "cata-server.log"
	// DirServerLogs server 相关日志（cata-server.log + llm.log 及其产出区分文件）的统一根目录。
	// 放在已有 ~/.cata/logs 下，方便与 agent / supervisor 日志一起集中排查。
	DirServerLogs = "server"
	// DirLLMLogs 按产出区拆分的 LLM 请求日志目录（位于 DirServerLogs 之下）。
	DirLLMLogs = "llm"
	// DirLocks ArchiveSessionLogs 等跨进程互斥锁目录。
	DirLocks = "locks"
)

// serverLogsDir 返回 server 相关日志的根目录（CATA_HOME/logs/server）。
func serverLogsDir() string {
	return filepath.Join(config.LogsDir(), DirServerLogs)
}

// ServerLogPath 返回 server 日志绝对路径（~/.cata/logs/server/cata-server.log）。
func ServerLogPath() string {
	return filepath.Join(serverLogsDir(), FileServerLog)
}

// LLMLogPath 返回当前（全局产出区）LLM 日志绝对路径。
func LLMLogPath() string {
	return LLMLogPathFor(OutputCwd())
}

// LLMLogPathFor 返回指定产出区对应的 LLM 日志绝对路径。
// 显式设置 LLM_LOG_FILE 时保持单文件（兼容旧行为）；否则按产出区拆分到
// ~/.cata/logs/server/llm/<sanitized>.log，多 cata 并行时各 chat 的请求日志不互相串写。
func LLMLogPathFor(outCwd string) string {
	if p := strings.TrimSpace(os.Getenv("LLM_LOG_FILE")); p != "" {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(CataHome(), p)
	}
	if out := strings.TrimSpace(outCwd); out != "" {
		return filepath.Join(serverLogsDir(), DirLLMLogs, llmLogFileName(outCwd))
	}
	return filepath.Join(serverLogsDir(), FileLLMLog)
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
// 便于本次写入新文件。首次在新布局下启动会把旧布局（CATA_HOME 根 / ~/.cata/llm）的
// 既有日志迁移到 logs/server/ 下再归档。用跨进程锁避免多个 server 实例同时迁移/归档互相踩踏。
func ArchiveSessionLogs() error {
	if err := os.MkdirAll(serverLogsDir(), 0755); err != nil {
		return err
	}
	if err := acquireArchiveLock(); err != nil {
		// 拿不到锁说明别的实例正在归档：等待其完成即可，不重复归档。
		return waitArchiveLockRelease()
	}
	defer releaseArchiveLock()

	// 把旧布局日志搬进新目录，随后统一归档（幂等：新布局已有同名文件时跳过，不覆盖）。
	var first error
	if err := migrateLegacyLogs(); err != nil && first == nil {
		first = err
	}
	if err := archiveLogFileIfExists(ServerLogPath()); err != nil && first == nil {
		first = err
	}
	if err := archiveLogFileIfExists(LLMLogPath()); err != nil && first == nil {
		first = err
	}
	llmDir := filepath.Join(serverLogsDir(), DirLLMLogs)
	if matches, err := filepath.Glob(filepath.Join(llmDir, "*.log")); err == nil {
		for _, p := range matches {
			if err := archiveLogFileIfExists(p); err != nil && first == nil {
				first = err
			}
		}
	}
	// 清理旧归档（name.YYYYMMDD-HHMMSS-RRR.ext），保留最近 maxArchivedLogs 个，防止无限堆积。
	pruneArchivedLogs(serverLogsDir())
	pruneArchivedLogs(llmDir)
	return first
}

// migrateLegacyLogs 把旧日志布局迁移到新统一目录 logs/server/：
//   - CATA_HOME 根下的 cata-server.log* → logs/server/cata-server.log*
//   - CATA_HOME 根下的 llm.log*        → logs/server/llm.log*
//   - CATA_HOME/llm/ 下的 *.log*       → logs/server/llm/*.log*
//
// 幂等：目标已存在同名文件时跳过该迁移项（新布局数据优先，不覆盖）。迁移完成后
// 由调用方（ArchiveSessionLogs）统一归档，保证每次启动仍写入全新的活跃文件。
func migrateLegacyLogs() error {
	srcGlobs := []struct {
		glob string
		dst  string
	}{
		{filepath.Join(CataHome(), FileServerLog+"*"), serverLogsDir()},
		{filepath.Join(CataHome(), FileLLMLog+"*"), serverLogsDir()},
		{filepath.Join(CataHome(), DirLLMLogs, "*.*"), filepath.Join(serverLogsDir(), DirLLMLogs)},
	}
	var first error
	for _, s := range srcGlobs {
		matches, err := filepath.Glob(s.glob)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		for _, src := range matches {
			st, err := os.Stat(src)
			if err != nil {
				if !os.IsNotExist(err) && first == nil {
					first = err
				}
				continue
			}
			if st.IsDir() {
				continue
			}
			dst := filepath.Join(s.dst, filepath.Base(src))
			// 目标已存在（新布局也有同名文件）：不覆盖，跳过。
			if _, err := os.Stat(dst); err == nil {
				continue
			}
			// llm 子目录可能尚不存在，预建后移动。
			_ = os.MkdirAll(s.dst, 0755)
			if err := os.Rename(src, dst); err != nil {
				if first == nil {
					first = err
				}
			}
		}
	}
	return first
}

// reArchivedLog 匹配归档日志文件名特征：name.YYYYMMDD-HHMMSS-RRR.ext（archivedLogPath 生成）。
var reArchivedLog = regexp.MustCompile(`\.\d{8}-\d{6}-\d{3}\.`)

const maxArchivedLogs = 10

// pruneArchivedLogs 删除目录下旧的归档日志，保留最近 keep 个（文件名含时间戳，字典序即时间序）。
func pruneArchivedLogs(dir string) {
	matches, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		return
	}
	var archived []string
	for _, p := range matches {
		if reArchivedLog.MatchString(filepath.Base(p)) {
			archived = append(archived, p)
		}
	}
	if len(archived) <= maxArchivedLogs {
		return
	}
	sort.Strings(archived)
	for _, p := range archived[:len(archived)-maxArchivedLogs] {
		_ = os.Remove(p)
	}
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
