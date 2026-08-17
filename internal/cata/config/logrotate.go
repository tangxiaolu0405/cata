package config

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
)

// 运行进程日志（agent/supervisor/gateway 的 stdout/stderr 重定向）会无限增长。
// 这些是纯流水，超大时截断到「尾部最近一段」，保留排查上下文的同时防止 ~/.cata 膨胀。
const (
	maxProcessLogBytes = 2 * 1024 * 1024 // 超过 2MB 触发截断
	keepProcessLogTail = 128 * 1024      // 保留尾部 128KB
)

// RotateRuntimeLogs 截断超大的运行进程日志：logs/*.log（agent + supervisor）+ cata-gateway.log。
// 幂等、按阈值触发；由 supervisor 周期调用，也可由 gateway 等长驻进程自行调用。
func RotateRuntimeLogs() {
	if matches, err := filepath.Glob(filepath.Join(LogsDir(), "*.log")); err == nil {
		for _, p := range matches {
			truncateLogKeepTail(p)
		}
	}
	truncateLogKeepTail(filepath.Join(CataHome(), "cata-gateway.log"))
}

// truncateLogKeepTail 若文件超过阈值，则读尾部 keepProcessLogTail 字节写回文件头并截断。
// 进程用 O_APPEND 写日志，truncate 安全（下次 write 从新末尾续写）。
func truncateLogKeepTail(path string) {
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() <= maxProcessLogBytes {
		return
	}
	off := st.Size() - keepProcessLogTail
	if off < 0 {
		off = 0
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return
	}
	tail := make([]byte, keepProcessLogTail)
	n, _ := io.ReadFull(f, tail)
	tail = tail[:n]
	// 从下一行起点开始保留，避免截断在半行；若换行恰在末尾，保留整个 tail。
	if idx := bytes.IndexByte(tail, '\n'); idx >= 0 && idx+1 < len(tail) {
		tail = tail[idx+1:]
	}
	if len(tail) == 0 {
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return
	}
	if _, err := f.Write(tail); err != nil {
		return
	}
	_ = f.Truncate(int64(len(tail)))
}
