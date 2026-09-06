package gateway

import (
	"os"
	"path/filepath"
	"strings"
)

// HandleWorkdirCommand 处理渠道会话（telegram / qq / web）的产出区切换命令 /dir：
//   - arg 为空 → 返回当前会话产出区（cwd），不切换
//   - 否则解析为绝对路径（~ 展开）、校验为**存在的目录**，把会话连接切换到该 cwd
//
// 切换后后续 chat 请求以新 cwd 发出：server 端按 cwd 解析产出区并绑定脑子格
// （传统 server 走 ResolveWorkspace，与 `cata chat --dir <path>` 等价），
// 从而在 IM 渠道里沿用本地 TUI 用过的项目工作目录。
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
	// GetWithCwd 在 cwd 变化时重建会话连接：后续 chat 用新产出区。
	if _, err := sessions.GetWithCwd(key, abs); err != nil {
		return "切换失败: " + err.Error(), true
	}
	return "产出区已切换:\n" + cur + "\n→\n" + abs +
		"\n\n接下来的对话在此目录工作（按 git / workspace.yaml 绑定脑子格）。\n/clear 可清空本会话历史。", true
}
