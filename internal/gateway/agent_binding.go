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

// AgentBinding 全局「渠道消息转发目标 agent」绑定（QQ/TG 等所有渠道共用同一个）。
//
// 语义：
//   - gateway 只做消息转发：按绑定决定把消息发给哪个 agent（工作空间）；
//   - 首次使用由用户通过 /dir 选择绑定哪一个；
//   - 目标 agent 不在线（不在 supervisor）时由转发方拉起（link.EnsureAgent）；
//   - 内存 cache 只是缓存：更换时**先 save 到配置文件 → 删除内存缓存 → 再从配置文件读取**，
//     保证重启后、以及多次切换后都以配置文件为准。
type AgentBinding struct {
	mu    sync.Mutex
	path  string
	cache string // 内存缓存（空 = 未加载/未绑定）
}

type agentBindingFile struct {
	AgentID   string `json:"agent_id"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// NewAgentBinding 从 path 加载（文件缺失视为未绑定）。
func NewAgentBinding(path string) *AgentBinding {
	return &AgentBinding{path: path}
}

// DefaultAgentBinding 默认绑定文件：~/.cata/gateway_channel_agent.json。
func DefaultAgentBinding() *AgentBinding {
	return NewAgentBinding(filepath.Join(config.CataHome(), "gateway_channel_agent.json"))
}

// Agent 返回当前绑定 agent（内存缓存优先；缓存空则从配置文件读取并缓存）。
// 返回 "" = 未绑定。
func (b *AgentBinding) Agent() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cache != "" {
		return b.cache
	}
	b.loadLocked()
	return b.cache
}

// Set 更换绑定。严格顺序：
//  1. 先 save 到配置文件；
//  2. 删除内存缓存；
//  3. 从配置文件读取（返回重载结果）。
//
// agentID 为空 = 解绑。返回重新加载后的绑定（"" = 未绑定/失败）。
func (b *AgentBinding) Set(agentID string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	agentID = strings.TrimSpace(agentID)
	if !b.saveLocked(agentID) {
		return b.cache // 写配置失败：保持原缓存
	}
	b.cache = "" // 删除内存缓存
	b.loadLocked()
	return b.cache
}

// Clear 解绑（同 Set("")）。
func (b *AgentBinding) Clear() string {
	return b.Set("")
}

func (b *AgentBinding) loadLocked() {
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
	b.cache = strings.TrimSpace(f.AgentID)
}

func (b *AgentBinding) saveLocked(agentID string) bool {
	if b.path == "" {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(b.path), 0755); err != nil {
		return false
	}
	f := agentBindingFile{AgentID: agentID}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return false
	}
	return os.WriteFile(b.path, raw, 0644) == nil
}

// AgentBindingTarget 返回当前绑定 agent 及其工作空间根路径。
// ok=false = 未绑定或绑定失效（agent 不在注册表）。
func AgentBindingTarget(b *AgentBinding) (agentID, root string, ok bool) {
	if b == nil {
		return "", "", false
	}
	agentID = b.Agent()
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

// HandleAgentBindCommand 处理渠道绑定命令 /dir（全局绑定一个 agent）：
//   - arg 为空 → 列出可用 agent（工作区）+ 当前绑定标记
//   - arg 为序号 → 绑定候选列表中的 agent（须先看列表）
//   - arg 为路径 → 解析路径对应的工作区并绑定其 agent
//   - arg 为 reset → 解绑（回到未绑定，下次消息会引导重新选择）
//
// 返回 (回复文本, 是否已处理)。
func HandleAgentBindCommand(b *AgentBinding, key SessionKey, arg string) (string, bool) {
	cur := ""
	if b != nil {
		cur = b.Agent()
	}
	arg = strings.TrimSpace(arg)
	if arg == "" {
		// 查看列表即「确认过序号依据」：之后才允许 /dir <序号> 绑定。
		MarkAgentListSeen()
		return agentBindMenu(cur), true
	}
	if strings.EqualFold(arg, "reset") {
		b.Clear()
		return "已解绑。下次消息会先让你选择要绑定的 agent（/dir 查看列表）。", true
	}
	agentID := ""
	// 序号选择：候选按最近使用排序，须先看列表确认。
	if n, err := strconv.Atoi(arg); err == nil && n >= 1 {
		if !dirListSeenFor() {
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
		return "已绑定 agent: " + agentID, true
	}
	// 严格顺序：先 save → 删除内存缓存 → 从配置文件读取。
	loaded := b.Set(agentID)
	reply := "已绑定 agent: " + agentID
	if loaded != agentID {
		reply += "\n（注意：配置文件写入后读取结果不一致，以配置为准: " + loaded + "）"
	}
	return reply, true
}

// agentBindMenu 当前绑定 + 可用 agent 列表。
func agentBindMenu(cur string) string {
	var b strings.Builder
	if cur != "" {
		fmt.Fprintf(&b, "当前绑定 agent: %s\n\n", cur)
	} else {
		b.WriteString("尚未绑定 agent —— 首次使用请选一个要绑定的工作空间（消息会转发给它）:\n\n")
	}
	cands := workspaceCandidates()
	if len(cands) == 0 {
		b.WriteString("暂无已注册工作区 — 用 /dir <绝对路径> 绑定其所在工作区。")
		return b.String()
	}
	b.WriteString("已注册工作区（发 /dir <序号> 绑定，如 /dir 1）:\n")
	for i, e := range cands {
		label := strings.TrimSpace(e.Name)
		if label == "" {
			label = e.ID
		}
		mark := " "
		if e.ID == cur {
			mark = "●"
		}
		fmt.Fprintf(&b, "%d. %s %s — %s（%s）\n", i+1, mark, label, e.RootPath, relSeen(e.LastSeenAt))
	}
	b.WriteString("\n绑定后 QQ/TG 消息统一转发到该 agent；/dir reset 解绑。")
	return b.String()
}

// dirListSeenFor 序号绑定前的列表确认（进程内存，重启后需重新查看）。
// 绑定是全局的，列表确认全局一次即可：本进程首次 /dir 查看列表后放行序号绑定。
var (
	agentListSeenMu sync.Mutex
	agentListSeen   bool
)

func dirListSeenFor() bool {
	agentListSeenMu.Lock()
	defer agentListSeenMu.Unlock()
	return agentListSeen
}

// ResetAgentListSeen 仅测试用：重置列表确认标记（模拟新进程）。
func ResetAgentListSeen() {
	agentListSeenMu.Lock()
	defer agentListSeenMu.Unlock()
	agentListSeen = false
}

// MarkAgentListSeen 记录本进程已查看过 agent 列表（HandleAgentBindCommand 无参分支调用）。
func MarkAgentListSeen() {
	agentListSeenMu.Lock()
	defer agentListSeenMu.Unlock()
	agentListSeen = true
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
