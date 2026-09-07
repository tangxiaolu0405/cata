package gateway

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
	"cata/internal/cata/link"
)

// ConnForMessage 返回该会话的转发连接（**纯转发模型**：消息转发到 /dir 选定工作空间的 agent）。
//
// 目标 agent 解析：
//   - 会话有 /dir 切换的产出区（CwdOverride）→ 解析该路径所在工作空间（ws_id）作为转发目标；
//   - 无 /dir 切换 → 默认 worker 目录（{worker_root}/{channel}/{chat_id}/），
//     转发到 default_agent_id（配置）或第一个注册工作空间的 agent。
//
// 本地模式：DialLocalAgent 一键拉起目标 agent 并拨其 per-ws socket
// （消息由该工作空间的 agent 接收处理，cwd = 其工作空间根路径）。
// remote 模式：拨目标 agent 的隧道（remoteDial 回退默认代理连接）。
//
// 会话按 (渠道会话键) 复用一条连接（history per-连接）；/dir 切换后连接重建。
func (sessions *SessionManager) ConnForMessage(cfg Config, channel string, key SessionKey) (*CataConn, error) {
	// /dir 切换的产出区：解析其工作空间 agent 作为转发目标。
	if dir := sessions.CwdOverride(key); dir != "" {
		ws, err := brain.ResolveWorkspaceNoGlobal(dir)
		if err != nil || ws == nil || strings.TrimSpace(ws.ID) == "" {
			return nil, fmt.Errorf("%s 渠道产出区 %q 不在任何工作空间 —— 请重新 /dir 选择", channel, dir)
		}
		agentID, root := ws.ID, ws.RootPath
		if sessions.IsRemote() {
			if sessions.remoteDial != nil {
				if d := sessions.remoteDial(agentID); d != nil {
					log.Printf("channel %q sessions: remote forward -> agent=%s cwd=%s", channel, agentID, root)
					return sessions.GetWithCwdDialer(key, root, d)
				}
			}
			return sessions.GetWithCwd(key, root)
		}
		if err := link.EnsureAgent(agentID); err != nil {
			return nil, fmt.Errorf("拉起 agent %s 失败: %v", agentID, err)
		}
		conn, err := sessions.GetWithCwdDialer(key, root, DialLocalAgent(agentID))
		if err != nil {
			return nil, err
		}
		log.Printf("channel %q sessions: forward -> agent=%s socket=%s cwd=%s",
			channel, agentID, config.ResolvedAgentSocketPath(agentID), root)
		return conn, nil
	}

	// 无 /dir 切换：默认转发目标（default_agent_id 或第一个注册工作空间）。
	agentID, root, ok := sessions.ResolveForwardTarget(cfg)
	if !ok {
		return nil, fmt.Errorf("%s 渠道无可用转发目标 —— 请先 /dir 选择工作空间，或确认已注册工作空间 agent", channel)
	}
	if sessions.IsRemote() {
		if sessions.remoteDial != nil {
			if d := sessions.remoteDial(agentID); d != nil {
				log.Printf("channel %q sessions: remote forward -> agent=%s cwd=%s", channel, agentID, root)
				return sessions.GetWithCwdDialer(key, root, d)
			}
		}
		return sessions.GetWithCwd(key, root)
	}
	if err := link.EnsureAgent(agentID); err != nil {
		return nil, fmt.Errorf("拉起 agent %s 失败: %v", agentID, err)
	}
	conn, err := sessions.GetWithCwdDialer(key, root, DialLocalAgent(agentID))
	if err != nil {
		return nil, err
	}
	log.Printf("channel %q sessions: forward -> agent=%s socket=%s cwd=%s",
		channel, agentID, config.ResolvedAgentSocketPath(agentID), root)
	return conn, nil
}

// ResolveForwardTarget 返回默认转发目标 agent 及其工作空间根路径。
// 优先 cfg.DefaultAgentID（注册且目录存在），否则第一个注册且目录存在的 agent。
func (sessions *SessionManager) ResolveForwardTarget(cfg Config) (agentID, root string, ok bool) {
	// remote 模式：优先 default_agent_id，否则第一个在线 agent。
	if sessions.remote {
		if id := strings.TrimSpace(cfg.DefaultAgentID); id != "" && sessions.reg != nil && sessions.reg.AgentAlive(id) {
			if info, ok := sessions.reg.FindAgent(id); ok {
				return id, info.RootPath, true
			}
		}
		if sessions.reg != nil {
			agents := sessions.reg.OnlineAgents()
			if len(agents) > 0 {
				return agents[0].AgentID, agents[0].RootPath, true
			}
		}
		return "", "", false
	}
	// 本地模式：default_agent_id 优先，否则第一个注册且目录存在的 agent。
	if id := strings.TrimSpace(cfg.DefaultAgentID); id != "" {
		if e, err := findRegistryEntryByID(id); err == nil && e != nil && dirExists(e.RootPath) {
			return id, e.RootPath, true
		}
	}
	entries, err := listRegistryEntries()
	if err != nil {
		return "", "", false
	}
	for _, e := range entries {
		if dirExists(e.RootPath) {
			return e.ID, e.RootPath, true
		}
	}
	return "", "", false
}

func dirExists(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// listRegistryEntries 返回已注册工作区（root_path 非空）。
func listRegistryEntries() ([]brain.RegistryEntry, error) {
	entries, err := brain.ListRegistryEntries()
	if err != nil {
		return nil, err
	}
	var out []brain.RegistryEntry
	for _, e := range entries {
		if strings.TrimSpace(e.RootPath) == "" {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// findRegistryEntryByID 按 id 查找注册表条目。
func findRegistryEntryByID(id string) (*brain.RegistryEntry, error) {
	entries, err := brain.ListRegistryEntries()
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if entries[i].ID == id {
			return &entries[i], nil
		}
	}
	return nil, nil
}

// resolveDirWorkspace 解析路径所在工作空间（供 /dir 命令用）。
func resolveDirWorkspace(dir string) (agentID, root string, err error) {
	abs := expandHomePath(dir)
	if abs == "" {
		return "", "", fmt.Errorf("无法解析路径: %s", dir)
	}
	ws, err := brain.ResolveWorkspaceNoGlobal(abs)
	if err != nil || ws == nil || strings.TrimSpace(ws.ID) == "" {
		return "", "", fmt.Errorf("路径不在任何工作空间: %s", abs)
	}
	return ws.ID, ws.RootPath, nil
}

func expandHomePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" || p == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return home
	}
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, strings.TrimPrefix(p, "~/"))
	}
	return p
}
