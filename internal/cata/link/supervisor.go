package link

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

	"cata/internal/cata/config"
)

// Supervisor 每机器一个：只管注册工作空间的 agent 进程生命周期（拉起/保活/停止），
// 不转发对话、不持有隧道（隧道由各 agent 进程自己持有）。
type Supervisor struct {
	socketPath string
	mu         sync.Mutex
	ln         net.Listener
}

// NewSupervisor 创建 supervisor 实例。
func NewSupervisor() *Supervisor {
	return &Supervisor{socketPath: config.SupervisorSocketPath()}
}

// RunSupervisor 前台运行 supervisor 守护（cata supervisor）。
//   - 启动时确保所有启用+常驻的 agent 在运行
//   - 监听 supervisor.sock 控制接口（ensure/stop/list/status/ping）
//   - 每 30s 复查常驻 agent 并补拉
//
// 阻塞直到 ctx 取消或收到 SIGINT/SIGTERM。
func RunSupervisor(ctx context.Context) error {
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
	_, err := EnsureAll()
	return err
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
		}
		if err := json.Unmarshal(line, &req); err != nil {
			_ = writeSupervisorResp(conn, false, "invalid request", nil)
			continue
		}
		var resp respBody
		switch req.Command {
		case "ping":
			resp = respBody{Success: true, Message: "pong"}
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
	_ = os.Remove(pidPath)
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
