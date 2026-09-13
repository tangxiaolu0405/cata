package link

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
	"cata/internal/cata/socketclient"
)

// PingAgentSocket 探测指定 agent socket 是否存活（2s 超时）。
func PingAgentSocket(socketPath string) error {
	return socketclient.Ping(socketPath)
}

// EnvAgentSupervised 环境变量：置 1 表示本 agent 由 supervisor（机器级隧道）按需拉起，
// supervisor 一死它就退出（孤儿清理）；客户端拉起的 agent 不置位，因此不会因为
// 「用户没跑 supervisor」而在 30 秒后自杀。
const EnvAgentSupervised = "CATA_AGENT_SUPERVISED"

// EnsureAgent 幂等拉起某工作空间的 agent 进程：
//   - per-ws socket 已存活 → 直接返回
//   - 否则持 per-ws spawn 锁（locks/ws-<id>.lock）启动 `cata agent --workspace <id>`，
//     注册且启用的 agent 带 --keep-alive（受 supervisor 生命周期监管）
//   - 最多等待 20s 直到 per-ws socket 可 ping
//
// 这是**客户端入口**（cata chat / 本地 gateway）的按需拉起落点：不因 supervisor 缺席而
// 自杀，由自身空闲回收（默认半小时无消息）兜底。supervisor 侧见 EnsureAgentSupervised。
func EnsureAgent(agentID string) error {
	return ensureAgent(agentID, false)
}

// EnsureAgentSupervised supervisor / 机器级隧道路径的按需拉起：拉起的 agent 归 supervisor 管
// （supervisor 死后随之退出，不留孤儿）。
func EnsureAgentSupervised(agentID string) error {
	return ensureAgent(agentID, true)
}

func ensureAgent(agentID string, supervised bool) error {
	socketPath := config.ResolvedAgentSocketPath(agentID)
	if PingAgentSocket(socketPath) == nil {
		return nil
	}
	return withSpawnLock(agentID, func() error {
		if PingAgentSocket(socketPath) == nil {
			return nil
		}
		cfg, err := LoadConfig()
		if err != nil {
			return err
		}
		entry, ok := cfg.Agents[agentID]
		keepAlive := ok && entry.Enabled && entry.KeepAlive

		exe, err := os.Executable()
		if err != nil {
			return err
		}
		args := []string{"agent", "--workspace", agentID}
		if keepAlive {
			args = append(args, "--keep-alive")
		}
		cmd := exec.Command(exe, args...)
		if supervised {
			cmd.Env = append(os.Environ(), EnvAgentSupervised+"=1")
		}
		cmd.Stdout = nil
		cmd.Stderr = nil
		detachCmd(cmd)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start cata agent %s: %w", agentID, err)
		}

		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if PingAgentSocket(socketPath) == nil {
				return nil
			}
			time.Sleep(200 * time.Millisecond)
		}
		// 20s 未就绪：杀子进程，避免残留半启动状态的 agent 占着后续资源。
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return fmt.Errorf("agent %s not ready after 20s (pid %d)", agentID, cmd.Process.Pid)
	})
}

// StopAgent 停止某工作空间的 agent 进程：发送 SIGTERM 给持有该 per-ws socket 的进程。
// 通过 supervisor.sock 控制接口执行（见 supervisor.go）。
func StopAgent(agentID string) error {
	return supervisorStopAgent(agentID)
}

// withSpawnLock 每个 agent 一把独立 spawn 锁（不同工作空间可并行拉起）。
// 锁文件内含持有者 pid；持有者已死（崩溃遗留）时回收陈旧锁，避免永久死锁。
func withSpawnLock(agentID string, fn func() error) error {
	dir := filepath.Join(brain.CataHome(), "locks")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, "ws-"+config.SanitizeSocketID(agentID)+".lock")
	socketPath := config.ResolvedAgentSocketPath(agentID)
	for i := 0; i < 100; i++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
			runErr := fn()
			_ = f.Close()
			_ = os.Remove(path)
			return runErr
		}
		if !os.IsExist(err) {
			return err
		}
		// 陈旧锁回收：持有者进程已死则删除锁文件，下轮重试拿到锁。
		if holder := readLockPID(path); holder > 0 && !processAlive(holder) {
			log.Printf("withSpawnLock: removing stale lock %s (pid %d dead)", path, holder)
			_ = os.Remove(path)
			continue
		}
		if PingAgentSocket(socketPath) == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("agent spawn lock timeout: %s", agentID)
}

// readLockPID 读取锁文件内的 pid；失败返回 0。
func readLockPID(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil || pid <= 0 {
		return 0
	}
	return pid
}

// processAlive 用 signal 0 探测进程是否存活（跨平台：Unix 下有效，Windows 下 FindProcess 近似）。
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
