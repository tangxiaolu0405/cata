package gateway

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"cata/internal/cata/brain"
	"cata/internal/cata/link"
)

// HandleWorkdirCommand 处理渠道会话（telegram / qq / web）的产出区/消费者切换命令 /dir：
//   - arg 为空 → 列出当前产出区 + 本机已注册工作区候选（/dir <序号> 一键切换）
//   - arg 为序号（1..N）→ 按候选列表切换（无需记住路径）
//   - arg 为路径 → 直接切换（~ 展开，必须是存在的目录）
//   - arg 为 reset → 恢复默认 worker 产出区
//
// 切换结果**持久化**（SessionCwdStore）：gateway 重启自动恢复，直到再次切换或 reset。
// 切换后后续 chat 请求以新 cwd 发出、会话连接拨到该工作区的 per-ws agent
// （即 QQ/TG 消息的「消费者」变成具体工作目录的 cata）。
// 目标 agent「不在线」时本地模式自动拉起（link.EnsureAgent）。
// 返回 (回复文本, 是否已处理)。任何失败保留原 cwd 不变。
func HandleWorkdirCommand(sessions *SessionManager, key SessionKey, arg string) (string, bool) {
	cur := sessions.CurrentCwd(key)
	if cur == "" {
		// 尚无会话：建立会话（Get 优先持久化切换，否则默认 worker cwd）。
		conn, err := sessions.Get(key)
		if err != nil {
			return "工作区错误: " + err.Error(), true
		}
		cur = conn.Cwd()
	}
	arg = strings.TrimSpace(arg)
	if arg == "" {
		// 查看列表即「确认过序号依据」：之后才允许 /dir <序号>。
		sessions.MarkDirListSeen(key)
		return workdirMenu(cur), true
	}
	if strings.EqualFold(arg, "reset") {
		// 清持久化 + 会话切回默认 worker 产出区。
		cwd, err := WorkerCwdForSession(sessions.workerRoot, key)
		if err != nil {
			return "恢复默认失败: " + err.Error(), true
		}
		sessions.SetCwdOverride(key, "")
		if _, err := sessions.GetWithCwd(key, cwd); err != nil {
			return "恢复默认失败: " + err.Error(), true
		}
		return "已恢复默认产出区: " + cwd, true
	}
	// 序号选择：候选列表按最近使用排序，用户无需记住完整路径。
	// 安全限制：未先发 /dir 查看列表不允许序号切换（序号会随使用变化，须先确认）。
	if n, err := strconv.Atoi(arg); err == nil && n >= 1 {
		if !sessions.DirListSeen(key) {
			return "请先发 /dir 查看工作区列表（序号按最近使用排序，确认后再用 /dir <序号> 切换）", true
		}
		cands := workspaceCandidates()
		if n > len(cands) || len(cands) == 0 {
			return fmt.Sprintf("序号无效: %d（先发 /dir 查看可用工作区）", n), true
		}
		arg = cands[n-1].RootPath
	}
	abs := arg
	var err error
	if abs == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "解析 home 失败: " + err.Error(), true
		}
		abs = home
	} else if strings.HasPrefix(abs, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "解析 home 失败: " + err.Error(), true
		}
		abs = filepath.Join(home, strings.TrimPrefix(abs, "~/"))
	}
	abs, err = filepath.Abs(abs)
	if err != nil {
		return "路径解析失败: " + err.Error(), true
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "目录不存在: " + abs, true
	}
	if !fi.IsDir() {
		return "不是目录: " + abs, true
	}
	if abs == cur {
		return "已在产出区: " + cur, true
	}
	// 本地模式：解析目标工作区并确保其 agent 在线（目标「不在线」也自动拉起）。
	assured := false
	if !sessions.IsRemote() {
		if ws, werr := brain.ResolveWorkspaceNoGlobal(abs); werr == nil && ws != nil && strings.TrimSpace(ws.ID) != "" {
			if aerr := link.EnsureAgent(ws.ID); aerr == nil {
				// 会话连接拨该工作区的 per-ws agent socket：消息消费者 = 该工作区 cata。
				if _, err := sessions.GetWithCwdDialer(key, abs, DialLocalAgent(ws.ID)); err == nil {
					assured = true
				}
			}
		}
	}
	if !assured {
		// 非项目目录 / remote 模式 / 拉起失败：回退默认会话连接（worker socket）。
		if _, err := sessions.GetWithCwd(key, abs); err != nil {
			return "切换失败: " + err.Error(), true
		}
	}
	// 持久化：重启后自动恢复该会话的产出区（直到再次切换或 /dir reset）。
	sessions.SetCwdOverride(key, abs)
	reply := "产出区已切换:\n" + cur + "\n→\n" + abs +
		"\n\n接下来的对话在此目录工作（按 git / workspace.yaml 绑定脑子格）。"
	if assured {
		reply += "\n工作区 agent 已自动就绪（在线）。"
	}
	reply += "\n（已记住该切换，重启后仍生效；/dir reset 可恢复默认产出区）"
	return reply, true
}

// maxWorkdirCandidates /dir 列表最多列出的已注册工作区数。
const maxWorkdirCandidates = 10

// workspaceCandidates 本机已注册工作区（按最近使用倒序，最多 maxWorkdirCandidates）。
func workspaceCandidates() []brain.RegistryEntry {
	entries, err := brain.ListRegistryEntries()
	if err != nil {
		return nil
	}
	var out []brain.RegistryEntry
	for _, e := range entries {
		if strings.TrimSpace(e.RootPath) == "" {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return lastSeenTime(out[i].LastSeenAt).After(lastSeenTime(out[j].LastSeenAt))
	})
	if len(out) > maxWorkdirCandidates {
		out = out[:maxWorkdirCandidates]
	}
	return out
}

func lastSeenTime(rfc string) time.Time {
	ts, err := time.Parse(time.RFC3339, rfc)
	if err != nil {
		return time.Time{}
	}
	return ts
}

// workdirMenu /dir 无参输出：当前 + 候选列表（支持 /dir <序号>）。
func workdirMenu(cur string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "当前产出区: %s\n\n", cur)
	cands := workspaceCandidates()
	if len(cands) == 0 {
		b.WriteString("暂无已注册工作区 — 用 /dir <绝对路径> 手动切换（支持 ~/）。")
		return b.String()
	}
	b.WriteString("已注册工作区（发 /dir <序号> 切换，如 /dir 1）:\n")
	for i, e := range cands {
		label := strings.TrimSpace(e.Name)
		if label == "" {
			label = e.ID
		}
		fmt.Fprintf(&b, "%d. %s — %s（%s）\n", i+1, label, e.RootPath, relSeen(e.LastSeenAt))
	}
	b.WriteString("\n/路径 可直接切换；/dir reset 恢复默认产出区。")
	return b.String()
}

// relSeen LastSeenAt 相对时间（x 分钟 / x 小时 / 日期）。
func relSeen(rfc string) string {
	ts := lastSeenTime(rfc)
	if ts.IsZero() {
		return "未知"
	}
	d := time.Since(ts)
	switch {
	case d < 0:
		return "刚刚"
	case d < 1*time.Minute:
		return "刚刚"
	case d < 1*time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	}
}
