package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
)

// AgentBinding 按渠道的消息转发目标（agent）绑定：**一个渠道绑定一个 agent**，
// 各渠道互不影响（如 telegram 绑 A、qq 绑 B）。
//
// 语义：
//   - gateway 只做消息转发：某渠道的消息发到该渠道绑定的 agent（工作空间）；
//   - 首次使用由用户通过 /dir 为该渠道选择绑定的 agent；
//   - 目标 agent 不在线（不在 supervisor）时由转发方拉起（link.EnsureAgent）；
//   - 内存 cache 只是缓存：更换时**先 save 到配置文件 → 删除内存缓存 → 再从配置文件读取**，
//     保证重启后、以及多次切换后都以配置文件为准。
type AgentBinding struct {
	mu    sync.Mutex
	path  string
	cache map[string]string // channel -> agentID（内存缓存，空 = 未加载/未绑定）
}

// agentBindingFile 配置文件结构：按渠道的绑定映射。
type agentBindingFile struct {
	ByChannel map[string]string `json:"by_channel,omitempty"`
}

// NewAgentBinding 从 path 加载（文件缺失视为未绑定）。
func NewAgentBinding(path string) *AgentBinding {
	return &AgentBinding{path: path}
}

// DefaultAgentBinding 默认绑定文件：~/.cata/gateway_channel_agent.json。
func DefaultAgentBinding() *AgentBinding {
	return NewAgentBinding(filepath.Join(config.CataHome(), "gateway_channel_agent.json"))
}

// Agent 返回 channel 渠道当前绑定的 agent（内存缓存优先；缓存空则从配置文件读取并缓存）。
// 返回 "" = 该渠道未绑定。
func (b *AgentBinding) Agent(channel string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cache == nil {
		b.loadLocked()
	}
	return b.cache[channel]
}

// Set 更换 channel 渠道的绑定。严格顺序：
//  1. 先 save 到配置文件；
//  2. 删除内存缓存；
//  3. 从配置文件读取（返回该渠道重载结果）。
//
// agentID 为空 = 解绑该渠道。
func (b *AgentBinding) Set(channel, agentID string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	agentID = strings.TrimSpace(agentID)
	if !b.saveLocked(channel, agentID) {
		if b.cache != nil {
			return b.cache[channel] // 写配置失败：保持原缓存
		}
		return ""
	}
	b.cache = nil // 删除内存缓存
	b.loadLocked()
	return b.cache[channel]
}

// Clear 解绑 channel 渠道（同 Set(channel, "")）。
func (b *AgentBinding) Clear(channel string) string {
	return b.Set(channel, "")
}

func (b *AgentBinding) loadLocked() {
	b.cache = map[string]string{}
	if b.path == "" {
		return
	}
	raw, err := os.ReadFile(b.path)
	if err != nil {
		return
	}
	var f agentBindingFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return
	}
	for ch, id := range f.ByChannel {
		b.cache[ch] = strings.TrimSpace(id)
	}
}

func (b *AgentBinding) saveLocked(channel, agentID string) bool {
	if b.path == "" {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(b.path), 0755); err != nil {
		return false
	}
	// 基于当前磁盘状态更新该渠道，保留其它渠道绑定。
	f := agentBindingFile{ByChannel: map[string]string{}}
	if raw, err := os.ReadFile(b.path); err == nil {
		_ = json.Unmarshal(raw, &f)
	}
	if f.ByChannel == nil {
		f.ByChannel = map[string]string{}
	}
	if agentID == "" {
		delete(f.ByChannel, channel)
	} else {
		f.ByChannel[channel] = agentID
	}
	out, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return false
	}
	return os.WriteFile(b.path, out, 0644) == nil
}

// AgentBindingTarget 返回 channel 渠道当前绑定 agent 及其工作空间根路径。
// ok=false = 该渠道未绑定或绑定失效（agent 不在注册表）。
func AgentBindingTarget(b *AgentBinding, channel string) (agentID, root string, ok bool) {
	if b == nil {
		return "", "", false
	}
	agentID = b.Agent(channel)
	if agentID == "" {
		return "", "", false
	}
	if e, err := findRegistryEntryByID(agentID); err == nil && e != nil {
		return agentID, e.RootPath, true
	}
	return "", "", false
}

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

// HandleAgentBindCommand 处理某渠道的绑定命令 /dir（**按渠道单一绑定**）：
//   - arg 为空 → 列出该渠道当前绑定 + 可用 agent（须先看列表才能用序号）
//   - arg 为序号 → 绑定候选列表中的 agent（须先看过列表）
//   - arg 为路径 → 解析路径对应的工作区并绑定其 agent
//   - arg 为 reset → 解绑该渠道
//
// 返回 (回复文本, 是否已处理)。
func HandleAgentBindCommand(b *AgentBinding, channel string, key SessionKey, arg string) (string, bool) {
	cur := ""
	if b != nil {
		cur = b.Agent(channel)
	}
	arg = strings.TrimSpace(arg)
	if arg == "" {
		// 查看列表即「确认过序号依据」：之后才允许 /dir <序号> 绑定。
		MarkAgentListSeen(channel)
		return agentBindMenu(b, channel, cur), true
	}
	if strings.EqualFold(arg, "reset") {
		b.Clear(channel)
		return "已解绑 " + channel + " 渠道的 agent。下次消息会先让你选择要绑定的 agent（/dir 查看列表）。", true
	}
	agentID := ""
	// 序号选择：候选按最近使用排序，须先看列表确认。
	if n, err := strconv.Atoi(arg); err == nil && n >= 1 {
		if !dirListSeenFor(channel) {
			return "请先发 /dir 查看 agent 列表（序号按最近使用排序，确认后再用 /dir <序号> 绑定）", true
		}
		cands := workspaceCandidates()
		if n > len(cands) || len(cands) == 0 {
			return fmt.Sprintf("序号无效: %d（先发 /dir 查看可用 agent）", n), true
		}
		agentID = cands[n-1].ID
	} else {
		// 路径：解析其工作区（focus_path），绑定对应 agent。
		abs := expandHomePath(arg)
		if abs == "" {
			return "无法解析路径: " + arg, true
		}
		ws, err := brain.ResolveWorkspaceNoGlobal(abs)
		if err != nil || ws == nil || strings.TrimSpace(ws.ID) == "" {
			return "路径不在任何工作区: " + abs, true
		}
		agentID = ws.ID
	}
	if agentID == "" {
		return "未选择有效 agent", true
	}
	if agentID == cur {
		return "该渠道已绑定 agent: " + agentID, true
	}
	// 严格顺序：先 save → 删除内存缓存 → 从配置文件读取。
	loaded := b.Set(channel, agentID)
	reply := "已绑定 " + channel + " 渠道 → agent: " + agentID
	if loaded != agentID {
		reply += "\n（注意：配置文件写入后读取结果不一致，以配置为准: " + loaded + "）"
	}
	return reply, true
}

// agentBindMenu 某渠道当前绑定 + 可用 agent 列表（全渠道绑定一并标注）。
func agentBindMenu(b *AgentBinding, channel, cur string) string {
	var sb strings.Builder
	if cur != "" {
		fmt.Fprintf(&sb, "%s 渠道当前绑定 agent: %s\n\n", channel, cur)
	} else {
		fmt.Fprintf(&sb, "%s 渠道尚未绑定 agent —— 首次使用请选一个要绑定的工作空间（该渠道消息会转发给它）:\n\n", channel)
	}
	cands := workspaceCandidates()
	if len(cands) == 0 {
		sb.WriteString("暂无已注册工作区 — 用 /dir <绝对路径> 绑定其所在工作区。")
		return sb.String()
	}
	sb.WriteString("已注册工作区（发 /dir <序号> 绑定到本渠道，如 /dir 1）:\n")
	for i, e := range cands {
		label := strings.TrimSpace(e.Name)
		if label == "" {
			label = e.ID
		}
		mark := " "
		if e.ID == cur {
			mark = "●"
		}
		fmt.Fprintf(&sb, "%d. %s %s — %s（%s）\n", i+1, mark, label, e.RootPath, relSeen(e.LastSeenAt))
	}
	sb.WriteString("\n每个渠道单独绑定一个 agent（telegram / qq 互不影响）；/dir reset 解绑本渠道。")
	return sb.String()
}

// 序号绑定前的列表确认：**按渠道**各自一次即可（本进程内）。
var (
	agentListSeenMu sync.Mutex
	agentListSeen   = map[string]bool{}
)

func dirListSeenFor(channel string) bool {
	agentListSeenMu.Lock()
	defer agentListSeenMu.Unlock()
	return agentListSeen[channel]
}

// MarkAgentListSeen 记录 channel 渠道已查看过 agent 列表。
func MarkAgentListSeen(channel string) {
	agentListSeenMu.Lock()
	defer agentListSeenMu.Unlock()
	agentListSeen[channel] = true
}

// ResetAgentListSeen 仅测试用：重置某渠道列表确认标记（模拟新进程）。
func ResetAgentListSeen(channel string) {
	agentListSeenMu.Lock()
	defer agentListSeenMu.Unlock()
	delete(agentListSeen, channel)
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
