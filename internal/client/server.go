package client

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"cata/internal/brain"
	"cata/internal/config"
)

// PingServer checks cata.sock responds to ping.
func PingServer() error {
	if err := config.InitBrainPath(); err != nil {
		return err
	}
	conn, err := net.DialTimeout("unix", config.ResolvedSocketPath(), 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	req, _ := json.Marshal(map[string]string{"command": "ping"})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return err
	}
	var resp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(line), &resp); err != nil {
		return err
	}
	if !resp.Success || resp.Message != "pong" {
		return fmt.Errorf("bad ping: %s", line)
	}
	return nil
}

// EnsureServer starts `cata run --managed` if needed.
func EnsureServer() error {
	if err := PingServer(); err == nil {
		return nil
	}
	return withSpawnLock(startAndWaitServer)
}

func startAndWaitServer() error {
	if err := PingServer(); err == nil {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "run", "--managed")
	cmd.Stdout = nil
	cmd.Stderr = nil
	detachCmd(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if err := PingServer(); err == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("server not ready after 20s")
}

func withSpawnLock(fn func() error) error {
	dir := filepath.Join(brain.CataHome(), "locks")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, "spawn.lock")
	for i := 0; i < 30; i++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
			err := fn()
			_ = f.Close()
			_ = os.Remove(path)
			return err
		}
		if !os.IsExist(err) {
			return err
		}
		if PingServer() == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("spawn lock timeout")
}
