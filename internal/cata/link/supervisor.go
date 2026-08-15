package link

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
)

// Supervisor 每机器一个：只管注册工作空间的 agent 进程生命周期（拉起/保活/停止），
// 不转发对话、不持有隧道（隧道由各 agent 进程自己持有）。
type Supervisor struct {
	socketPath string
	mu         sync.Mutex
	ln         net.Listener
	backoff    *ensureBackoff
}

// NewSupervisor 创建 supervisor 实例。
func NewSupervisor() *Supervisor {
	return &Supervisor{
		socketPath: config.SupervisorSocketPath(),
		backoff:    newEnsureBackoff(),
	}
}

// RunSupervisor 前台运行 supervisor 守护（cata supervisor）。
//   - 启动时确保所有启用+常驻的 agent 在运行
//   - 监听 supervisor.sock 控制接口（ensure/stop/list/status/ping）
//   - 每 30s 复查常驻 agent 并补拉（带失败退避：连续失败暂停该 agent）
//
// 阻塞直到 ctx 取消或收到 SIGINT/SIGTERM。
func RunSupervisor(ctx context.Context) error {
	// 守护化后 stdout/stderr 为 nil：日志必须落盘，否则排查问题只能靠猜。
	redirectSupervisorLogs()

	s := NewSupervisor()
	if err := s.ensureAll(); err != nil {
		log.Printf("cata supervisor: initial ensure: %v", err)
	}

	// 控制 socket 单例：已有 supervisor 在跑则退出。
	ln, acquired, err := acquireSupervisorLock(s.socketPath)
	if err != nil {
		return err
	}
	if !acquired {
		return fmt.Errorf("cata supervisor already running (%s)", s.socketPath)
	}
	defer ln.Close()
	defer os.Remove(s.socketPath)
	s.ln = ln

	log.Printf("cata supervisor: control socket %s", s.socketPath)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		select {
		case <-sig:
			cancel()
		case <-ctx.Done():
		}
	}()

	// 控制接口
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					continue
				}
			}
			go s.handleConn(conn)
		}
	}()

	// 常驻保活复查
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("cata supervisor: stopped")
			return nil
		case <-ticker.C:
			if err := s.ensureAll(); err != nil {
				log.Printf("cata supervisor: ensure all: %v", err)
			}
		}
	}
}

func (s *Supervisor) ensureAll() error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	// 自动接入本机已有工作空间（git/marked），避免用户逐个手动 link add。
	if err := autoLinkExistingWorkspaces(cfg); err != nil {
		log.Printf("supervisor: auto-link: %v", err)
		cfg, err = LoadConfig() // 重载，纳入刚自动注册的 agent
		if err != nil {
			return err
		}
	}
	ids := cfg.LinkedAgentIDs()
	for _, id := range ids {
		e := cfg.Agents[id]
		if !e.Enabled {
			continue
		}
		// 带崩溃退避：单个失败不阻断其它 agent，连续失败暂停补拉。
		if err := s.backoff.ensure(id); err != nil {
			log.Printf("supervisor: ensure agent %s: %v", id, err)
			continue
		}
	}
	return nil
}

// autoLinkExistingWorkspaces 扫描 ~/.cata/brain/workspaces 下的所有工作空间
// （新旧项目都算，跳过 .cata_worker 渠道沙箱），把尚未注册到 link.json 的自动
// Add（keep-alive），使本机已有项目自动接入 gateway，避免逐个手动接入。
func autoLinkExistingWorkspaces(cfg Config) error {
	wsList, err := brain.ListHomeWorkspaces()
	if err != nil {
		return err
	}
	linked := cfg.Agents
	added := 0
	for _, w := range wsList {
		if w.ID == "" || w.RootPath == "" {
			continue
		}
		if isHomeRootPath(w.RootPath) {
			log.Printf("supervisor: auto-link skip %s (root_path is home dir)", w.ID)
			continue
		}
		if _, exists := linked[w.ID]; exists {
			continue
		}
		if _, err := Add(w.RootPath, true); err != nil {
			log.Printf("supervisor: auto-link %s: %v", w.ID, err)
			continue
		}
		added++
	}
	if added > 0 {
		log.Printf("supervisor: auto-linked %d existing workspace(s)", added)
	}
	return nil
}

func (s *Supervisor) handleConn(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReaderSize(conn, 64*1024)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var req struct {
			Command string `json:"command"`
			AgentID string `json:"agent_id"`
			Subpath string `json:"subpath,omitempty"` // add 命令：相对 workspace_root 的子路径
		}
		if err := json.Unmarshal(line, &req); err != nil {
			_ = writeSupervisorResp(conn, false, "invalid request", nil)
			continue
		}
		var resp respBody
		switch req.Command {
		case "ping":
			resp = respBody{Success: true, Message: "pong"}
		case "add":
			// 注册一个新工作空间：校验 subpath 在 workspace_root 下，写 link.json 并拉起 agent。
			if err := addWorkspaceRemote(req.Subpath); err != nil {
				resp = respBody{Success: false, Message: err.Error()}
			} else {
				resp = respBody{Success: true, Message: "registered"}
			}
		case "ensure":
			agentID := strings.TrimSpace(req.AgentID)
			if agentID == "" {
				resp = respBody{Success: false, Message: "ensure: agent_id required"}
				break
			}
			if err := EnsureAgent(agentID); err != nil {
				resp = respBody{Success: false, Message: err.Error()}
			} else {
				resp = respBody{Success: true, Message: "ok", Data: map[string]any{"agent_id": agentID, "alive": AgentAlive(agentID)}}
			}
		case "stop":
			agentID := strings.TrimSpace(req.AgentID)
			if agentID == "" {
				resp = respBody{Success: false, Message: "stop: agent_id required"}
				break
			}
			if err := killAgentProcess(agentID); err != nil {
				resp = respBody{Success: false, Message: err.Error()}
			} else {
				resp = respBody{Success: true, Message: "stopped"}
			}
		case "list", "status":
			entries, _ := List()
			type row struct {
				AgentID   string `json:"agent_id"`
				Name      string `json:"name"`
				RootPath  string `json:"root_path,omitempty"`
				KeepAlive bool   `json:"keep_alive"`
				Enabled   bool   `json:"enabled"`
				Alive     bool   `json:"alive"`
			}
			rows := make([]row, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, row{
					AgentID: e.AgentID, Name: e.Name, RootPath: e.RootPath,
					KeepAlive: e.KeepAlive, Enabled: e.Enabled, Alive: AgentAlive(e.AgentID),
				})
			}
			resp = respBody{Success: true, Message: "ok", Data: map[string]any{"agents": rows}}
		default:
			resp = respBody{Success: false, Message: fmt.Sprintf("unknown command: %s", req.Command)}
		}
		_ = writeSupervisorResp(conn, resp.Success, resp.Message, resp.Data)
	}
}

type respBody struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func writeSupervisorResp(conn net.Conn, success bool, msg string, data interface{}) error {
	b, err := json.Marshal(respBody{Success: success, Message: msg, Data: data})
	if err != nil {
		return err
	}
	_, err = conn.Write(append(b, '\n'))
	return err
}

// acquireSupervisorLock 尝试监听 supervisor.sock；已被占用说明有 supervisor 在跑。
func acquireSupervisorLock(path string) (net.Listener, bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, false, err
	}
	// 清理陈旧 socket
	if _, err := os.Stat(path); err == nil {
		if conn, err := net.DialTimeout("unix", path, 500*time.Millisecond); err == nil {
			conn.Close()
			return nil, false, nil // 已有 supervisor 存活
		}
		_ = os.Remove(path)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, false, err
	}
	return ln, true, nil
}

// SupervisorStopAgent 通过 supervisor.sock 让 supervisor 停止某 agent。
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

// addWorkspaceRemote 解析 register 路径、确保目录存在（不存在则创建）、注册工作空间并拉起 agent。
// 由 supervisor 控制接口的 add 命令调用（也经 worker 隧道 register 帧转交）。
func addWorkspaceRemote(subpath string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	dir, err := ResolveWorkspacePath(cfg, subpath)
	if err != nil {
		return err
	}
	// 幂等：目录已存在则跳过创建；不存在则创建（仅限 workspace_root 下，越界已在 ResolveWorkspacePath 拦截）。
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create workspace dir: %w", err)
	}
	// 注册进 link.json（keep-alive 常驻），并立即拉起 agent + supervisor。
	entry, err := Add(dir, true)
	if err != nil {
		return fmt.Errorf("link add: %w", err)
	}
	if err := EnsureAgent(entry.AgentID); err != nil {
		return fmt.Errorf("ensure agent: %w", err)
	}
	return nil
}

// HandleRemoteRegister worker 侧处理网关 register 控制帧：解析路径、确保目录存在、
// 经 supervisor.sock 转交 supervisor 执行 add（写 link.json + 拉起 agent）。
// 之所以经 supervisor 而非直接 Add，是为了复用 supervisor 已有的保活/退避/生命周期语义。
func HandleRemoteRegister(subpath string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	dir, err := ResolveWorkspacePath(cfg, subpath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create workspace dir: %w", err)
	}
	// 经 supervisor 控制接口执行 add（若 supervisor 未跑则直接本地 Add + Ensure）。
	if SupervisorAlive() {
		return supervisorAdd(subpath)
	}
	entry, err := Add(dir, true)
	if err != nil {
		return err
	}
	return EnsureAgent(entry.AgentID)
}

// supervisorAdd 通过 supervisor.sock 下发 add 命令。
func supervisorAdd(subpath string) error {
	conn, err := net.DialTimeout("unix", config.SupervisorSocketPath(), 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	req, _ := json.Marshal(map[string]string{"command": "add", "subpath": subpath})
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
		return fmt.Errorf("supervisor add: %s", resp.Message)
	}
	return nil
}

// isHomeRootPath 判断 root_path 是否就是用户 home 目录本身（或 CATA_HOME）。
// 这类"整个家目录当工作空间"的格子（如 users-lucas）不该自动接入——接入后
// agent 会绑定到 home，能读写 ~/.ssh、~/.cata 等敏感内容。
func isHomeRootPath(rootPath string) bool {
	p := filepath.Clean(rootPath)
	home, err := os.UserHomeDir()
	if err == nil && filepath.Clean(home) == p {
		return true
	}
	if cata := config.CataHome(); cata != "" && filepath.Clean(cata) == p {
		return true
	}
	return false
}
