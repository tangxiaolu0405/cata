package gateway

import (
	"os"
	"path/filepath"
	"strings"

	"cata/internal/cata/brain"
	"cata/internal/cata/link"
)

// HandleWorkdirCommand 处理渠道会话（telegram / qq / web）的产出区切换命令 /dir：
//   - arg 为空 → 返回当前会话产出区（cwd），不切换
//   - 否则解析为绝对路径（~ 展开）、校验为**存在的目录**，把会话连接切换到该 cwd
//
// 切换后后续 chat 请求以新 cwd 发出：server 端按 cwd 解析产出区并绑定脑子格
// （与 `cata chat --dir <path>` 等价），从而在 IM 渠道里沿用本地 TUI 用过的项目目录。
//
// 目标目录的 agent「不在线」时：本地模式会自动解析其工作区并拉起 per-ws agent
// （link.EnsureAgent），切换完成即可直接对话；解析失败（非项目目录）回退默认
// worker socket。remote 模式保持拨默认云端 agent。
// 返回 (回复文本, 是否已处理)。任何失败保留原 cwd 不变。
func HandleWorkdirCommand(sessions *SessionManager, key SessionKey, arg string) (string, bool) {
	cur := sessions.CurrentCwd(key)
	if cur == "" {
		// 尚无会话：按默认 worker 目录建立会话（Get 回退到 worker cwd）。
		conn, err := sessions.Get(key)
		if err != nil {
			return "工作区错误: " + err.Error(), true
		}
		cur = conn.Cwd()
	}
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "当前产出区: " + cur + "\n\n切换: /dir <绝对路径>（支持 ~/ 前缀）", true
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
				// 会话连接拨该工作区的 per-ws agent socket：切换即在线。
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
	reply := "产出区已切换:\n" + cur + "\n→\n" + abs +
		"\n\n接下来的对话在此目录工作（按 git / workspace.yaml 绑定脑子格）。"
	if assured {
		reply += "\n工作区 agent 已自动就绪（在线）。"
	}
	reply += "\n/clear 可清空本会话历史。"
	return reply, true
}
