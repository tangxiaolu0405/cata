package link

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"cata/internal/cata/config"
)

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
