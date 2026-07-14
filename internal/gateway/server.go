package gateway

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cata/internal/cata/brain"
)

// ServerManager base 版：按配置拉起并可选回收本机 cata server。
type ServerManager struct {
	cfg        Config
	socketPath string
	mu         sync.Mutex
	cmd        *exec.Cmd
	started    bool
}

// NewServerManager 创建 server 管理器。
func NewServerManager(cfg Config) *ServerManager {
	return &ServerManager{
		cfg:        cfg,
		socketPath: cfg.SocketPath,
	}
}

// Ensure 若配置要求则确保 cata server 可用。
func (m *ServerManager) Ensure() error {
	if !m.cfg.ShouldAutoStartServer() {
		if err := Ping(m.socketPath); err != nil {
			log.Printf("cata-gateway: cata server not running (%s); start `cata run` or set edition=base", m.socketPath)
		}
		return nil
	}
	if err := Ping(m.socketPath); err == nil {
		log.Printf("cata-gateway: cata server already up (%s)", m.socketPath)
		return nil
	}
	return m.start()
}

// Stop 若由本 manager 拉起且配置了 stop_on_exit，则结束 server 进程。
func (m *ServerManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.started || m.cmd == nil || m.cmd.Process == nil {
		return
	}
	if !m.cfg.CataServer.StopOnExit {
		log.Printf("cata-gateway: leaving cata server running (stop_on_exit=false)")
		return
	}
	_ = m.cmd.Process.Kill()
	_, _ = m.cmd.Process.Wait()
	m.cmd = nil
	m.started = false
	log.Printf("cata-gateway: cata server stopped")
}

func (m *ServerManager) start() error {
	return withSpawnLock(m.socketPath, func() error {
		if err := Ping(m.socketPath); err == nil {
			return nil
		}
		bin, err := m.resolveBinary()
		if err != nil {
			return err
		}
		args := []string{"run"}
		if m.cfg.CataServer.Managed {
			args = append(args, "--managed")
		}
		cmd := exec.Command(bin, args...)
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start cata server: %w", err)
		}
		m.mu.Lock()
		m.cmd = cmd
		m.started = true
		m.mu.Unlock()

		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			if err := Ping(m.socketPath); err == nil {
				log.Printf("cata-gateway: started cata server pid=%d binary=%s args=%v", cmd.Process.Pid, bin, args)
				return nil
			}
			time.Sleep(200 * time.Millisecond)
		}
		return fmt.Errorf("cata server not ready after 30s (started %s)", bin)
	})
}

func (m *ServerManager) resolveBinary() (string, error) {
	if b := strings.TrimSpace(m.cfg.CataServer.Binary); b != "" {
		if st, err := os.Stat(b); err == nil && !st.IsDir() {
			return b, nil
		}
		return "", fmt.Errorf("cata_server.binary not found: %s", b)
	}
	return resolveCataBinary()
}

// EnsureCataServer 兼容旧调用：按配置确保 server（无进程回收）。
func EnsureCataServer(socketPath string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	cfg.SocketPath = socketPath
	return NewServerManager(cfg).Ensure()
}

func resolveCataBinary() (string, error) {
	if v := os.Getenv("CATA_BIN"); v != "" {
		if st, err := os.Stat(v); err == nil && !st.IsDir() {
			return v, nil
		}
		return "", fmt.Errorf("CATA_BIN not found: %s", v)
	}
	if p, err := exec.LookPath("cata"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("cata not found in PATH; install cata or set cata_server.binary / CATA_BIN")
}

func withSpawnLock(socketPath string, fn func() error) error {
	dir := filepath.Join(brain.CataHome(), "locks")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, "spawn.lock")
	for i := 0; i < 30; i++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
			runErr := fn()
			_ = f.Close()
			_ = os.Remove(path)
			return runErr
		}
		if !os.IsNotExist(err) {
			return err
		}
		if Ping(socketPath) == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("spawn lock timeout")
}
