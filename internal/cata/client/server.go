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
