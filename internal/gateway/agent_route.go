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

// ConnForMessage 返回该会话的转发连接（**纯转发模型**：消息转发到 /dir 绑定工作空间的 agent）。
//
// 规则：
//   - 会话必须已通过 /dir 绑定工作空间（CwdOverride 非空）才转发；
//   - 未绑定 → 报错引导用户先 /dir 选择（**不默认转发到任何 agent**）。
//
// 本地模式：DialLocalAgent 一键拉起目标 agent 并拨其 per-ws socket
// （消息由该工作空间的 agent 接收处理，cwd = 其工作空间根路径）。
// remote 模式：拨目标 agent 的隧道（remoteDial 回退默认代理连接）。
//
// 会话按 (渠道会话键) 复用一条连接（history per-连接）；/dir 切换后连接重建。
func (sessions *SessionManager) ConnForMessage(cfg Config, channel string, key SessionKey) (*CataConn, error) {
	// 必须已 /dir 绑定工作空间。
	dir := sessions.CwdOverride(key)
	if dir == "" {
		return nil, fmt.Errorf("%s 渠道尚未绑定工作空间 —— 请先发 /dir 选择（消息将转发到该工作空间的 agent）", channel)
	}
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
