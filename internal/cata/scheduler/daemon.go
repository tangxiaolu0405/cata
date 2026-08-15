package scheduler

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// 调度守护进程（cata schedule）的单例管理：守护进程 bind ~/.cata/schedules/daemon.sock
// 作为进程级单例锁；存在且可连接 = 有守护在运行。schedule_task 创建/启用任务后调用
// EnsureDaemonRunning 自动拉起守护，实现「chat 里指定、之后不用管、后台自动执行」。
const daemonSocketName = "daemon.sock"

// DaemonSocketPath 调度守护单例 socket 路径。
func DaemonSocketPath() string {
	return filepath.Join(Dir(), daemonSocketName)
}

// DaemonLogPath 调度守护日志路径。
func DaemonLogPath() string {
	return filepath.Join(Dir(), "daemon.log")
}

// DaemonAlive 探测调度守护是否在运行（dial 单例 socket）。
func DaemonAlive() bool {
	conn, err := net.DialTimeout("unix", DaemonSocketPath(), 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// daemonCommand 构造后台守护进程命令（可被测试替换）。
var daemonCommand = func() (name string, args []string) {
	exe, err := os.Executable()
	if err != nil {
		return "", nil
	}
	return exe, []string{"schedule"}
}

// EnsureDaemonRunning 若调度守护未在运行，则后台拉起 `cata schedule` 守护进程
// （setsid 脱离会话，日志写 ~/.cata/schedules/daemon.log）。返回是否新拉起。
// 用于 schedule_task 创建/启用任务后让用户「创建后不用管、后台自动执行」。
func EnsureDaemonRunning() (spawned bool, err error) {
	if DaemonAlive() {
		return false, nil
	}
	if err := os.MkdirAll(Dir(), 0755); err != nil {
		return false, err
	}
	name, args := daemonCommand()
	if name == "" {
		return false, fmt.Errorf("scheduler: no executable to spawn daemon")
	}
	logf, err := os.OpenFile(DaemonLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return false, err
	}
	cmd := exec.Command(name, args...)
	cmd.Env = os.Environ()
	cmd.Stdout = logf
	cmd.Stderr = logf
	detachCmd(cmd)
	if err := cmd.Start(); err != nil {
		logf.Close()
		return false, err
	}
	logf.Close()
	// 等守护 bind 单例 socket（最多 ~2s）；超时也视为已拉起（进程已启动）。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if DaemonAlive() {
			return true, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	log.Printf("scheduler: daemon process started (pid=%d) but socket not ready", cmd.Process.Pid)
	return true, nil
}

// AcquireDaemonLock 让守护进程独占单例 socket；返回是否获取成功。
// 已有活守护 → (nil, false, nil)；stale socket（守护被 kill 后残留）会清理后重试一次。
func AcquireDaemonLock() (net.Listener, bool, error) {
	if err := os.MkdirAll(Dir(), 0755); err != nil {
		return nil, false, err
	}
	path := DaemonSocketPath()
	ln, err := net.Listen("unix", path)
	if err == nil {
		return ln, true, nil
	}
	if DaemonAlive() {
		return nil, false, nil
	}
	// stale socket：清理后重试一次。
	_ = os.Remove(path)
	ln, err = net.Listen("unix", path)
	if err != nil {
		return nil, false, err
	}
	return ln, true, nil
}
