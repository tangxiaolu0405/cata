package link

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cata/internal/cata/config"
)

// supervisorStopAgent 通过 supervisor.sock 让 supervisor 停止某 agent。
// 先尝试 pid 文件直杀（不依赖 supervisor 是否在跑），再回退控制接口。
func supervisorStopAgent(agentID string) error {
	if err := killAgentProcess(agentID); err == nil {
		return nil
	}
	conn, err := net.DialTimeout("unix", config.SupervisorSocketPath(), 2*time.Second)
	if err != nil {
		return fmt.Errorf("agent %s stop: no pid file and supervisor not running", agentID)
	}
	defer conn.Close()
	req, _ := json.Marshal(map[string]string{"command": "stop", "agent_id": agentID})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return err
	}
	var resp respBody
	if err := json.Unmarshal(bytes.TrimSpace(line), &resp); err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("supervisor stop: %s", resp.Message)
	}
	return nil
}

// killAgentProcess 读取 pid 文件并 SIGTERM 对应 agent 进程（等待退出后删除 pid 文件）。
// 宽限期内未退出则升级 SIGKILL；只有确认进程退出才返回 nil，否则如实报错，
// 避免「残留进程占着 per-ws socket，后续 ensure 新进程 bind 冲突」的假成功。
func killAgentProcess(agentID string) error {
	pidPath := config.AgentPIDPath(agentID)
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return err
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil || pid <= 0 {
		_ = os.Remove(pidPath)
		return fmt.Errorf("invalid pid file")
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(pidPath)
		return err
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		_ = os.Remove(pidPath)
		return fmt.Errorf("process %d not running", pid)
	}
	_ = proc.Signal(syscall.SIGTERM)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			_ = os.Remove(pidPath)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	// SIGTERM 宽限超时：升级 SIGKILL，再等 2s 确认。
	_ = proc.Signal(syscall.SIGKILL)
	killDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(killDeadline) {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			_ = os.Remove(pidPath)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = os.Remove(pidPath)
	return fmt.Errorf("agent %s process %d did not exit after SIGTERM+SIGKILL", agentID, pid)
}

// StopSupervisor 向 supervisor 发 shutdown 命令：supervisor 退出并级联停掉全部保活 agent。
func StopSupervisor() error {
	conn, err := net.DialTimeout("unix", config.SupervisorSocketPath(), 2*time.Second)
	if err != nil {
		return fmt.Errorf("supervisor not running (%s)", config.SupervisorSocketPath())
	}
	defer conn.Close()
	req, _ := json.Marshal(map[string]string{"command": "shutdown"})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return err
	}
	var resp respBody
	if err := json.Unmarshal(bytes.TrimSpace(line), &resp); err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("supervisor shutdown: %s", resp.Message)
	}
	return nil
}

// SupervisorAlive 探测 supervisor 控制 socket 是否存活。
func SupervisorAlive() bool {
	conn, err := net.DialTimeout("unix", config.SupervisorSocketPath(), 500*time.Millisecond)
	if err != nil {
		return false
	}
	defer conn.Close()
	req, _ := json.Marshal(map[string]string{"command": "ping"})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return false
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return false
	}
	var resp respBody
	if err := json.Unmarshal(bytes.TrimSpace(line), &resp); err != nil {
		return false
	}
	return resp.Success && resp.Message == "pong"
}

// SupervisorWatchConfig keep-alive agent 对 supervisor 的存活探测参数。
// 关键：kill supervisor（含 SIGKILL）后 agent 不会收到任何信号（detachCmd 脱离进程组），
// 只能靠「supervisor 控制口失联」自检。
type SupervisorWatchConfig struct {
	// Interval 探测间隔（默认 5s；可用环境变量 CATA_SUPERVISOR_INTERVAL 覆盖，单位秒）。
	Interval time.Duration
	// Deadline 失联持续多久判定 supervisor 死亡（默认 30s；
	// 可用环境变量 CATA_SUPERVISOR_DEADLINE 覆盖，单位秒）。
	Deadline time.Duration
	// AliveFn 探测函数（默认 SupervisorAlive；测试注入）。
	AliveFn func() bool
	// Busy 返回当前是否有活跃会话（chat 流式/工具执行中）。非 nil 且返回 true 时，
	// 即使 supervisor 失联也**推迟**退出——避免正在进行的对话/任务被心跳误杀；
	// 等到空闲或 supervisor 恢复后再判。
	Busy func() bool
	// OnLost 失联超时 + 空闲（或无条件）时调用；默认内部 stop()（保持旧行为）。
	// 传入后由调用方决定：可改为「先尝试重启 supervisor、再决定是否退出」。
	OnLost func()
}

// supervisorWatchDefaults 读取环境变量覆盖（单位秒；非法/未设置回落代码默认）。
func supervisorWatchDefaults() (interval, deadline time.Duration) {
	interval = 5 * time.Second
	deadline = 30 * time.Second
	if v := os.Getenv("CATA_SUPERVISOR_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			interval = time.Duration(n) * time.Second
		}
	}
	if v := os.Getenv("CATA_SUPERVISOR_DEADLINE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			deadline = time.Duration(n) * time.Second
		}
	}
	return interval, deadline
}

// WatchSupervisorAndStop 后台监控 supervisor 存活；失联超过 Deadline 时触发
// OnLost（默认 stop()）。任何 kill supervisor 方式（含 SIGKILL）都能收敛，
// 避免「supervisor 死了 agent 变成孤儿继续占资源/持隧道」。
//
// 若配置了 Busy（有活跃会话返回 true）且当前忙碌，则**推迟**判定：继续探测，
// 直到空闲或 supervisor 恢复（防止正在进行的 chat/任务被误杀——EOF 根因）。
// 返回可直接 go 的闭包；stop 应为幂等（server.Stop 是幂等的）。
func WatchSupervisorAndStop(cfg SupervisorWatchConfig, stop func()) func() {
	// 优先级：显式传入 > 环境变量 > 代码默认。
	envInterval, envDeadline := supervisorWatchDefaults()
	interval := envInterval
	if cfg.Interval > 0 {
		interval = cfg.Interval
	}
	deadline := envDeadline
	if cfg.Deadline > 0 {
		deadline = cfg.Deadline
	}
	aliveFn := cfg.AliveFn
	if aliveFn == nil {
		aliveFn = SupervisorAlive
	}
	onLost := cfg.OnLost
	if onLost == nil {
		onLost = stop
	}
	return func() {
		lastAlive := time.Now()
		for {
			time.Sleep(interval)
			if aliveFn() {
				lastAlive = time.Now()
				continue
			}
			if time.Since(lastAlive) >= deadline {
				// 忙碌（有活跃会话）时不死机：日志提示并继续等到空闲。
				if cfg.Busy != nil && cfg.Busy() {
					log.Printf("supervisor unreachable for %s — busy chat session active, deferring shutdown", deadline)
					lastAlive = time.Now() // 重置：避免长期持会话时频繁刷日志
					continue
				}
				log.Printf("supervisor unreachable for %s — shutting down keep-alive agent", deadline)
				onLost()
				return
			}
		}
	}
}

// EnsureSupervisorDaemon 幂等拉起常驻 supervisor 守护（cata link add 后自动调用）。
func EnsureSupervisorDaemon() error {
	if SupervisorAlive() {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "supervisor")
	cmd.Stdout = nil
	cmd.Stderr = nil
	detachCmd(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if SupervisorAlive() {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("supervisor not ready after 15s")
}

// redirectSupervisorLogs 把标准 log 重定向到 ~/.cata/logs/supervisor.log。
// EnsureSupervisorDaemon 以 stdout/stderr=nil 拉起守护进程，不重定向则所有日志丢失。
func redirectSupervisorLogs() {
	path := config.SupervisorLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("supervisor: cannot open log file %s: %v", path, err)
		return
	}
	log.SetOutput(io.MultiWriter(f))
	log.Printf("supervisor: log redirected to %s", path)
}

// ensureAllWithBackoff 补拉常驻 agent，带崩溃退避：
// 同一 agent 连续失败 N 次后暂停补拉（sleep），防止「启动即崩」的 agent
// （如工作空间路径被删）被 30s ticker 无限重生刷日志。
type ensureBackoff struct {
	failCount map[string]int
	paused    map[string]time.Time
}

func newEnsureBackoff() *ensureBackoff {
	return &ensureBackoff{
		failCount: map[string]int{},
		paused:    map[string]time.Time{},
	}
}

// backoffPause 连续失败阈值与暂停时长。
const (
	backoffFailThreshold = 3
	backoffPauseDuration = 10 * time.Minute
)

// ensure 对单 agent 执行 ensure，并在失败时更新退避状态。
func (b *ensureBackoff) ensure(agentID string) error {
	if until, ok := b.paused[agentID]; ok {
		if time.Now().Before(until) {
			return fmt.Errorf("agent %s paused until %s (repeated failures)", agentID, until.Format("15:04:05"))
		}
		delete(b.paused, agentID)
		b.failCount[agentID] = 0
	}
	if err := EnsureAgent(agentID); err != nil {
		b.failCount[agentID]++
		if b.failCount[agentID] >= backoffFailThreshold {
			b.paused[agentID] = time.Now().Add(backoffPauseDuration)
			log.Printf("supervisor: agent %s failed %d times, pausing for %s", agentID, b.failCount[agentID], backoffPauseDuration)
		} else {
			log.Printf("supervisor: ensure agent %s: %v (fail %d/%d)", agentID, err, b.failCount[agentID], backoffFailThreshold)
		}
		return err
	}
	b.failCount[agentID] = 0
	return nil
}
