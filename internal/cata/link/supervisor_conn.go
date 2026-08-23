package link

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// handleConn 处理 supervisor 控制 socket 的请求循环（ping/add/ensure/stop/list/status）。
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
		case "shutdown":
			// 关闭 supervisor 并级联停掉全部保活 agent（cata supervisor stop）。
			resp = respBody{Success: true, Message: "shutting down"}
			if s.cancel != nil {
				s.cancel()
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
