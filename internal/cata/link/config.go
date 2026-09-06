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
	"fmt"
	"os"
	"path/filepath"
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
	// AgentToken per-agent 凭证（隧道 hello 层）：首次注册由网关按 machine 权威签发、
	// hello_ack 下发后落盘。空 = 尚未获得（回退 machine_token 连接）。
	AgentToken string `json:"agent_token,omitempty"`
}

// Config link.json：机器级网关注册配置（CATA_HOME/link.json）。
type Config struct {
	// GatewayURL 网关地址（如 https://gw.example.com 或 ws://127.0.0.1:8788）。空 = 未配置网关。
	GatewayURL string `json:"gateway_url,omitempty"`
	// GatewayToken 网关准入口令（HTTP 握手层第一道门，join 时提供）。与网关 gateway.json 的
	// gateway_token 一致。逐机器凭证见 MachineToken。
	GatewayToken string `json:"gateway_token,omitempty"`
	// AllowAgentIDs 网关卡白名单（v1 预留：空 = 服务端放行）。
	AllowAgentIDs []string `json:"allow_agent_ids,omitempty"`
	// DefaultAgentID 通道类会话（telegram/qq）在远端默认路由到的 agent（空 = 第一个在线）。
	DefaultAgentID string `json:"default_agent_id,omitempty"`
	// WorkspaceRoot 机器级工作空间根前缀：网关下发的 register 控制帧只能在此前缀之下
	// 注册工作空间（防越界，gateway 不暴露机器内部路径）。空 = 拒绝远程 register。
	WorkspaceRoot string `json:"workspace_root,omitempty"`
	// MachineID 本机稳定标识（join 时生成并持久化，用于逐机器 token 的键）。
	// 空 = 未 join，MachineID() 回退 hostname。
	MachineID string `json:"machine_id,omitempty"`
	// MachineToken 本机逐机器凭证（join 后由网关签发）。空 = 未 join。
	MachineToken string `json:"machine_token,omitempty"`
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
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// GatewayConfigured 是否已配置网关（gateway_url + 逐机器 token）。
func (c Config) GatewayConfigured() bool {
	return strings.TrimSpace(c.GatewayURL) != "" &&
		strings.TrimSpace(c.MachineToken) != ""
}

// HasAgent 是否注册了某工作空间。
func (c Config) HasAgent(agentID string) bool {
	_, ok := c.Agents[agentID]
	return ok
}

// AgentTokenFor 返回某 agent 的 per-agent token（未注册或未签发的空串）。
func (c Config) AgentTokenFor(agentID string) string {
	if e, ok := c.Agents[agentID]; ok {
		return strings.TrimSpace(e.AgentToken)
	}
	return ""
}

// SetAgentTokenFor 持久化某 agent 的 per-agent token（幂等：同值不重复写盘）。
func SetAgentTokenFor(agentID, token string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	if cfg.Agents == nil {
		cfg.Agents = map[string]AgentEntry{}
	}
	e := cfg.Agents[agentID]
	if strings.TrimSpace(e.AgentToken) == strings.TrimSpace(token) {
		return nil
	}
	e.AgentToken = strings.TrimSpace(token)
	cfg.Agents[agentID] = e
	return SaveConfig(cfg)
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

// Add 注册一个工作空间（由 cata link add / register 调用）：
//   - 解析目录 → 工作空间（注册进 registry + 落盘 link.json）
//   - 默认 keep-alive（注册即常驻）
//   - 幂等：同一 root_path 已注册时直接返回既有条目（不重复写、不刷新 linked_at）
//
// 注意：网关地址与逐机器 token 由 `cata link join` 预先写入 link.json，本函数不再接收。
func Add(dir string, keepAlive bool) (AgentEntry, error) {
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
	if cfg.Agents == nil {
		cfg.Agents = map[string]AgentEntry{}
	}
	// 幂等：已注册同一工作空间则直接返回，避免重复写盘 / 刷新 linked_at。
	if existing, ok := cfg.Agents[ws.ID]; ok {
		return existing, nil
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

// MachineID 返回本机稳定标识：优先 link.json 持久化的 machine_id（join 时生成），
// 回退 hostname。用于 hello 帧携带，gateway 据此按机器分组、校验逐机器 token。
func MachineID() string {
	if cfg, err := LoadConfig(); err == nil {
		if m := strings.TrimSpace(cfg.MachineID); m != "" {
			return m
		}
	}
	h, err := os.Hostname()
	if err != nil || strings.TrimSpace(h) == "" {
		return "unknown"
	}
	return strings.TrimSpace(h)
}

// ResolveWorkspacePath 解析 register 下发的路径，返回最终要绑定的绝对目录。
//
// 语义（gateway 作为入口，worker 侧校验）：
//   - 相对名（如 "abc"）→ 拼到 workspace_root 下（workspace_root/abc）；必须已配置 workspace_root
//   - 绝对路径 → 若目录已存在，直接绑定（允许在 workspace_root 之外，用于接入本机已有项目）；
//     若不存在，必须严格落在 workspace_root 之下才允许（防止 gateway 越界创建目录）
//   - 空输入 → 使用 workspace_root 自身
//
// 越界防护：拒绝 `..` 逃逸；未配置 workspace_root 时拒绝一切（除非是已存在的绝对路径目录）。
func ResolveWorkspacePath(cfg Config, subpath string) (string, error) {
	root := strings.TrimSpace(cfg.WorkspaceRoot)
	sub := strings.TrimSpace(subpath)

	// 相对名（或空）：必须配 workspace_root，拼前缀。
	if !filepath.IsAbs(sub) {
		if root == "" {
			return "", fmt.Errorf("workspace_root not configured; remote register disabled")
		}
		absRoot, err := filepath.Abs(root)
		if err != nil {
			return "", fmt.Errorf("workspace_root: %w", err)
		}
		if sub == "" {
			return absRoot, nil
		}
		joined := filepath.Join(absRoot, sub)
		rel, err := filepath.Rel(absRoot, joined)
		if err != nil {
			return "", fmt.Errorf("resolve subpath: %w", err)
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("subpath escapes workspace_root")
		}
		return joined, nil
	}

	// 绝对路径：已存在 → 直接绑定（不管是否在 root 下）；不存在 → 必须在 root 下才允许创建。
	abs := filepath.Clean(sub)
	if st, err := os.Stat(abs); err == nil && st.IsDir() {
		return abs, nil
	}
	if root == "" {
		return "", fmt.Errorf("workspace_root not configured; cannot create absolute path outside it")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("workspace_root: %w", err)
	}
	rel, err := filepath.Rel(absRoot, abs)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("absolute path outside workspace_root and does not exist; cannot create")
	}
	return abs, nil
}
