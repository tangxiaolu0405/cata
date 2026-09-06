package gateway

import (
	"fmt"

	"cata/internal/cata/link"
)

// replyForWorkdir 统一处理渠道的 /dir 命令（绑定 agent 转发模型）：
//   - /dir         → 查看/选择绑定的 agent（全局，QQ/TG 共用）
//   - /dir <序号>  → 绑定候选 agent（须先看过列表）
//   - /dir <路径>  → 绑定该路径所在工作区的 agent
//   - /dir reset   → 解绑
//
// 返回可供渠道直接发送的回复文本。
func ReplyForWorkdir(binding *AgentBinding, key SessionKey, arg string) string {
	reply, _ := HandleAgentBindCommand(binding, key, arg)
	return reply
}

// ConnForMessage 按绑定 agent 返回该会话的转发连接。
//   - 未绑定 → 错误（引导用户先 /dir 选择）
//   - 绑定但 agent 不在注册表 → 错误（引导重新绑定）
//   - 本地模式 → DialLocalAgent 一键拉起绑定 agent 并拨其 per-ws socket
//   - remote 模式 → 回退默认代理连接
//
// 会话按 (chat 渠道会话键) 复用一条连接（history per-连接），但 cwd 始终是绑定
// agent 的工作空间根路径；更换绑定后连接重建、以新 agent 转发。
func (sessions *SessionManager) ConnForMessage(binding *AgentBinding, key SessionKey) (*CataConn, error) {
	agentID, root, ok := AgentBindingTarget(binding)
	if !ok {
		if binding == nil || binding.Agent() == "" {
			return nil, fmt.Errorf("尚未绑定 agent —— 请先发 /dir 选择要绑定的工作空间（消息将转发给它）")
		}
		return nil, fmt.Errorf("绑定 agent %q 不在工作区注册表 —— 请 /dir 重新选择", binding.Agent())
	}
	if sessions.IsRemote() {
		// remote 模式：拨默认云端代理连接（绑定在部署侧由远端 agent 决定）。
		return sessions.GetWithCwd(key, root)
	}
	// 本地模式：确保绑定 agent 在线（不在 supervisor 则拉起），把会话拨到它。
	if err := link.EnsureAgent(agentID); err != nil {
		return nil, fmt.Errorf("拉起 agent %s 失败: %v", agentID, err)
	}
	return sessions.GetWithCwdDialer(key, root, DialLocalAgent(agentID))
}
