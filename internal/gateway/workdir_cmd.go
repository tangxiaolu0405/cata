package gateway

import (
	"fmt"
	"strconv"
	"strings"
)

// ReplyForWorkdir 统一处理渠道的 /dir 命令（**按会话切换工作空间** 转发模型）：
//   - /dir         → 查看/选择本会话的工作空间（列注册工作区，可序号或路径）
//   - /dir <序号>  → 切换到候选工作区（须先看过列表）
//   - /dir <路径>  → 切换到该路径所在工作区
//   - /dir reset   → 恢复默认 worker 目录
//
// 消息将转发到 /dir 选定工作空间的 agent；切换持久化（重启保持）。
func ReplyForWorkdir(sessions *SessionManager, channel string, key SessionKey, arg string) string {
	cur := sessions.CwdOverride(key)
	arg = strings.TrimSpace(arg)

	if arg == "" {
		// 查看列表即「确认过序号依据」：之后才允许 /dir <序号> 切换。
		sessions.MarkDirListSeen(key)
		return workdirMenu(sessions, channel, key, cur)
	}

	if strings.EqualFold(arg, "reset") {
		sessions.SetCwdOverride(key, "")
		return "已恢复默认产出区（worker 目录）。下次消息转发到默认 agent。"
	}

	// 序号切换：候选按最近使用排序，须先看列表确认。
	if n, err := strconv.Atoi(arg); err == nil && n >= 1 {
		if !sessions.DirListSeen(key) {
			return "请先发 /dir 查看工作区列表（序号按最近使用排序，确认后再用 /dir <序号> 切换）"
		}
		cands := workspaceCandidates()
		if n > len(cands) || len(cands) == 0 {
			return fmt.Sprintf("序号无效: %d（先发 /dir 查看可用工作区）", n)
		}
		cand := cands[n-1]
		return applyWorkdir(sessions, channel, key, cand.RootPath)
	}

	// 路径切换：解析其工作空间（focus_path）。
	_, root, err := resolveDirWorkspace(arg)
	if err != nil {
		return err.Error()
	}
	return applyWorkdir(sessions, channel, key, root)
}

// applyWorkdir 执行切换并返回结果文本。
func applyWorkdir(sessions *SessionManager, channel string, key SessionKey, root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return "未选择有效工作区"
	}
	cur := sessions.CwdOverride(key)
	if cur == root {
		return "本会话已在工作区: " + root
	}
	sessions.SetCwdOverride(key, root)
	reply := "已切换本会话工作区 → " + root
	reply += "\n（消息将转发到该工作空间的 agent；/dir reset 恢复默认）"
	_ = channel
	return reply
}

// workdirMenu 当前会话工作区 + 可用工作区列表。
func workdirMenu(sessions *SessionManager, channel string, key SessionKey, cur string) string {
	var sb strings.Builder
	if cur != "" {
		fmt.Fprintf(&sb, "本会话当前工作区: %s\n\n", cur)
	} else {
		fmt.Fprintf(&sb, "本会话当前使用默认产出区（worker 目录）。可切换的工作区:\n\n")
	}
	cands := workspaceCandidates()
	if len(cands) == 0 {
		sb.WriteString("暂无已注册工作区 — 用 /dir <绝对路径> 切换其所在工作区。")
		return sb.String()
	}
	sb.WriteString("已注册工作区（发 /dir <序号> 切换，如 /dir 1）:\n")
	for i, e := range cands {
		label := strings.TrimSpace(e.Name)
		if label == "" {
			label = e.ID
		}
		mark := " "
		if e.RootPath == cur {
			mark = "●"
		}
		fmt.Fprintf(&sb, "%d. %s %s — %s（%s）\n", i+1, mark, label, e.RootPath, relSeen(e.LastSeenAt))
	}
	sb.WriteString("\n/dir <绝对路径> 也可切换；/dir reset 恢复默认。")
	return sb.String()
}

// workdirApplyForTest 供测试直接切换（不经过菜单确认）。
func workdirApplyForTest(sessions *SessionManager, key SessionKey, root string) {
	sessions.SetCwdOverride(key, root)
}
