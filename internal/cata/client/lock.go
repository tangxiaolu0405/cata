package client

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cata/internal/cata/brain"
	"cata/internal/cata/clock"
)

type lockMeta struct {
	PID       int    `json:"pid"`
	OutputCwd string `json:"output_cwd"`
	StartedAt string `json:"started_at"`
}

// AcquireOutputLock 取某产出区的独占锁（一个产出区同时只有一个在写的工作会话）。
//
// 规则：
//   - 锁文件 ~/.cata/locks/out_<hash>.lock 里记 pid / cwd / 起始时间；
//   - 持有者进程已死（崩溃 / kill -9 遗留）→ 直接回收，不让用户手工删文件；
//   - 持有者仍存活 → 返回 busy 错误，并给出持有者 pid、开始时间与可操作提示。
//
// 谁按什么粒度持有：
//   - `cata chat` TUI：整个会话期间持有（一次只开一个 TUI）；
//   - `cata-gateway` Web 控制台：**每轮对话**持有（本轮结束立即释放），
//     不按会话缓存——否则常驻网关会把产出区永久锁死（TUI 永远打不开那个目录）。
func AcquireOutputLock(outputCwd string) (func(), error) {
	abs, err := filepath.Abs(strings.TrimSpace(outputCwd))
	if err != nil {
		return nil, err
	}
	path, err := outputLockPath(abs)
	if err != nil {
		return nil, err
	}

	for attempt := 0; attempt < 3; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0644)
		if err == nil {
			b, _ := json.Marshal(lockMeta{PID: os.Getpid(), OutputCwd: abs, StartedAt: clock.RFC3339()})
			_, _ = f.Write(b)
			return func() { _ = f.Close(); _ = os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		m := readLockMeta(path)
		// pid 已死 / 内容损坏 → 陈旧锁：回收后重试。
		if m.PID > 0 && processAlive(m.PID) {
			return nil, busyError(abs, path, m)
		}
		_ = os.Remove(path)
	}
	return nil, fmt.Errorf("cata: could not lock %s（%s）", abs, path)
}

// busyError 产出区被占用时的错误：带持有者信息与下一步怎么做（否则用户只能靠猜）。
func busyError(abs, lockPath string, m lockMeta) error {
	since := ""
	if strings.TrimSpace(m.StartedAt) != "" {
		since = "，自 " + m.StartedAt
	}
	return fmt.Errorf("cata: output area busy (%s, pid %d%s)：该产出区已被另一个 cata 会话占用"+
		"（cata chat TUI 在整个会话期间持有）。请先结束那边；确认它已不在使用后，"+
		"也可删除锁文件 %s 重试", abs, m.PID, since, lockPath)
}

// outputLockPath 产出区 → 锁文件路径（locks 目录按需创建）。
func outputLockPath(abs string) (string, error) {
	dir := filepath.Join(brain.CataHome(), "locks")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "out_"+hex.EncodeToString(sha256Sum(abs)[:8])+".lock"), nil
}

// readLockMeta 读取锁文件元信息；损坏/空文件返回零值（调用方按陈旧锁回收）。
func readLockMeta(path string) lockMeta {
	var m lockMeta
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// PruneStaleOutputLocks 清理持有者已死的产出区锁文件（崩溃 / kill -9 遗留），返回清理数量。
// TUI 启动时调用一次即可：既避免锁文件越积越多，也让 pid 恰好被系统复用时不会误判 busy。
func PruneStaleOutputLocks() int {
	dir := filepath.Join(brain.CataHome(), "locks")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	removed := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "out_") || !strings.HasSuffix(name, ".lock") {
			continue
		}
		path := filepath.Join(dir, name)
		m := readLockMeta(path)
		if m.PID > 0 && processAlive(m.PID) {
			continue // 仍被存活进程持有：不动
		}
		if os.Remove(path) == nil {
			removed++
		}
	}
	return removed
}

func sha256Sum(s string) []byte {
	h := sha256.Sum256([]byte(filepath.Clean(s)))
	return h[:]
}
