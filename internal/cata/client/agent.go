package client

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"cata/internal/cata/config"
)

// PingSocket 探测任意 cata Unix socket 是否存活（ping → pong）。
func PingSocket(socketPath string) error {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	req := []byte(`{"command":"ping"}`)
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

// AgentSocketPath 返回某工作空间的 per-ws agent socket 绝对路径。
func AgentSocketPath(wsID string) string {
	return config.ResolvedAgentSocketPath(wsID)
}
