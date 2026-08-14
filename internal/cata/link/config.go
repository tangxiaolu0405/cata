// Package link 管理「本地工作空间 → 远程网关」的注册与每工作空间 agent 进程生命周期。
//
// 架构（扁平化三层，用户已确认）：
//
//	gateway（云端、无状态注册中心+路由） ↔ 每机器 supervisor（只管进程生命周期） ↔
//	每工作空间一个 `cata agent` 进程（= 一个 LLM loop，自持到网关的 WSS 隧道）
//
// 隔离键 = agent_id = ws_id（工作空间 id）。注册（cata link add）过的项目常驻；
// 纯本地未注册的工作空间由 cata chat 按需拉起、空闲回收。
package link

import (
	"encoding/json"
	"os"
	"sort"
	"strings"

	"cata/internal/cata/brain"
	"cata/internal/cata/clock"
	"cata/internal/cata/config"
)

// AgentEntry 单个工作空间在 link.json 中的注册项。
type AgentEntry struct {
	AgentID   string `json:"agent_id"`
	RootPath  string `json:"root_path,omitempty"`
	Name      string `json:"name,omitempty"`
	KeepAlive bool   `json:"keep_alive,omitempty"` // 常驻：不因空闲退出（注册即常驻）
	Enabled   bool   `json:"enabled,omitempty"`
	LinkedAt  string `json:"linked_at,omitempty"`
}

// Config link.json：机器级网关注册配置（CATA_HOME/link.json）。
type Config struct {
	// GatewayURL 网关地址（如 https://gw.example.com 或 ws://127.0.0.1:8788）。空 = 未配置网关。
	GatewayURL string `json:"gateway_url,omitempty"`
	// Token 与网关共享的 Bearer token（v1 共享 token；逐 agent token 留 v2）。
	Token string `json:"token,omitempty"`
	// AllowAgentIDs 网关卡白名单（v1 预留：空 = 服务端放行）。
	AllowAgentIDs []string `json:"allow_agent_ids,omitempty"`
	// DefaultAgentID 通道类会话（telegram/qq）在远端默认路由到的 agent（空 = 第一个在线）。
	DefaultAgentID string `json:"default_agent_id,omitempty"`
	// Agents 已注册的工作空间（agent_id → 注册项）。
	Agents map[string]AgentEntry `json:"agents,omitempty"`
}

// LoadConfig 读取 link.json（不存在返回空配置）。
func LoadConfig() (Config, error) {
	if err := brain.EnsureCataLayout(); err != nil {
		return Config{}, err
	}
	var cfg Config
	data, err := os.ReadFile(config.LinkConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, err
	}
	_ = json.Unmarshal(data, &cfg)
	return cfg, nil
}

// SaveConfig 原子写回 link.json。
func SaveConfig(cfg Config) error {
	if err := brain.EnsureCataLayout(); err != nil {
		return err
	}
	if cfg.Agents == nil {
		cfg.Agents = map[string]AgentEntry{}
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := config.LinkConfigPath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// GatewayConfigured 是否已配置网关（gateway_url + token）。
func (c Config) GatewayConfigured() bool {
	return strings.TrimSpace(c.GatewayURL) != "" && strings.TrimSpace(c.Token) != ""
}

// HasAgent 是否注册了某工作空间。
func (c Config) HasAgent(agentID string) bool {
	_, ok := c.Agents[agentID]
	return ok
}

// ShouldKeepAlive 某工作空间是否应常驻（注册且 keep_alive）。
func (c Config) ShouldKeepAlive(agentID string) bool {
	e, ok := c.Agents[agentID]
	return ok && e.Enabled && e.KeepAlive
}

// TunnelEnabled 某工作空间是否应持有到网关的隧道（注册 + 启用 + 已配网关）。
func (c Config) TunnelEnabled(agentID string) bool {
	return c.GatewayConfigured() && c.ShouldKeepAlive(agentID)
}

// LinkedAgentIDs 已注册工作空间 id 列表（稳定排序）。
func (c Config) LinkedAgentIDs() []string {
	ids := make([]string, 0, len(c.Agents))
	for id := range c.Agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Add 注册一个工作空间（由 cata link add 调用）：
//   - 解析目录 → 工作空间（注册进 registry + 落盘 link.json）
//   - 默认 keep-alive（注册即常驻）
func Add(dir string, keepAlive bool, gatewayURL, token string) (AgentEntry, error) {
	if err := brain.EnsureCataLayout(); err != nil {
		return AgentEntry{}, err
	}
	ws, err := brain.ResolveWorkspace(dir)
	if err != nil {
		return AgentEntry{}, err
	}
	cfg, err := LoadConfig()
	if err != nil {
		return AgentEntry{}, err
	}
	if g := strings.TrimSpace(gatewayURL); g != "" {
		cfg.GatewayURL = g
	}
	if t := strings.TrimSpace(token); t != "" {
		cfg.Token = t
	}
	if cfg.Agents == nil {
		cfg.Agents = map[string]AgentEntry{}
	}
	name := ws.Name
	if name == "" {
		name = ws.ID
	}
	entry := AgentEntry{
		AgentID:   ws.ID,
		RootPath:  ws.RootPath,
		Name:      name,
		KeepAlive: keepAlive,
		Enabled:   true,
		LinkedAt:  clock.RFC3339(),
	}
	cfg.Agents[ws.ID] = entry
	if err := SaveConfig(cfg); err != nil {
		return AgentEntry{}, err
	}
	return entry, nil
}

// Remove 移除某工作空间的注册项。
func Remove(agentID string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	if cfg.Agents == nil {
		return nil
	}
	if _, ok := cfg.Agents[agentID]; !ok {
		return nil
	}
	delete(cfg.Agents, agentID)
	return SaveConfig(cfg)
}

// List 列出已注册工作空间（稳定排序）。
func List() ([]AgentEntry, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	ids := cfg.LinkedAgentIDs()
	out := make([]AgentEntry, 0, len(ids))
	for _, id := range ids {
		out = append(out, cfg.Agents[id])
	}
	return out, nil
}

// Stat 某工作空间注册信息 + agent 运行状态。
func Stat(agentID string) (AgentEntry, bool, bool, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return AgentEntry{}, false, false, err
	}
	e, ok := cfg.Agents[agentID]
	alive := AgentAlive(agentID)
	return e, ok, alive, nil
}

// EnsureAll 确保所有启用的注册工作空间 agent 在运行（supervisor 用）。
func EnsureAll() (started int, err error) {
	cfg, err := LoadConfig()
	if err != nil {
		return 0, err
	}
	ids := cfg.LinkedAgentIDs()
	for _, id := range ids {
		e := cfg.Agents[id]
		if !e.Enabled {
			continue
		}
		if err := EnsureAgent(id); err != nil {
			// 单个失败不阻断其它 agent。
			continue
		}
		if !AgentAlive(id) {
			started++
		}
	}
	return started, nil
}

// AgentAlive 探测某工作空间 agent 的 per-ws socket 是否存活。
func AgentAlive(agentID string) bool {
	return PingAgentSocket(config.ResolvedAgentSocketPath(agentID)) == nil
}
