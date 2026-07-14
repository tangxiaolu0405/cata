package llm

import "cata/internal/cata/config"

// Role 表示 LLM 用途（不同 role 可绑定不同模型；均回退到 llm.model）。
type Role string

const (
	RoleDefault   Role = "default"
	RoleChat      Role = "chat"      // 主对话 + 工具（强模型）
	RoleEvolution Role = "evolution" // 后台演进决策（弱/快模型）
	RoleSummarize Role = "summarize" // 摘要 / 会话压缩
	RoleWorker    Role = "worker"    // delegate_task 子 agent
)

// ResolveModelForRole 解析某 role 应使用的模型名（配置未加载时返回空）。
// 回退链：models[role] → models[default] → llm.model → 空（由 NewClientFromConfig 再兜底）。
func ResolveModelForRole(role Role) string {
	if config.Config == nil {
		return ""
	}
	return resolveModelForRole(config.Config.LLM, role)
}

// ModelsByRole 返回当前配置下各 role 解析后的模型（便于 status / 侧栏展示）。
func ModelsByRole() map[string]string {
	if config.Config == nil {
		return nil
	}
	roles := []Role{RoleChat, RoleEvolution, RoleSummarize, RoleWorker}
	out := make(map[string]string, len(roles)+1)
	base := config.Config.LLM.Model
	if base != "" {
		out["base"] = base
	}
	for _, r := range roles {
		if m := resolveModelForRole(config.Config.LLM, r); m != "" {
			out[string(r)] = m
		}
	}
	return out
}
