package brain

import (
	"os"
	"strings"
)

// 用户纠正信号：当用户对 agent 输出明确否定时，视为「最近命中的记忆可能错误/过时」，
// Evaluate 对该记忆降权。仅用强信号（明确否定），避免误判（如「不要着急」不算纠正）。
var correctionSignals = []string{
	"不对", "错了", "不是这样", "不是的", "说反了", "理解错了", "搞错了", "反了", "不对啊", "说错",
}

// DetectUserCorrection 检测 short-term 里最近的 user 消息是否包含明确纠正信号。
// 返回是否纠正 + 该 user 消息所在块的 RFC3339 时间戳（供 Evaluate 关联命中）。
func DetectUserCorrection(w *Workspace) (bool, string) {
	if w == nil {
		return false, ""
	}
	data, err := os.ReadFile(w.ShortTermPath())
	if err != nil {
		return false, ""
	}
	return scanShortTermCorrection(string(data))
}

func scanShortTermCorrection(body string) (bool, string) {
	turns := splitShortTermTurns(body)
	// 从后往前：只取最近的 user 消息判断（纠正通常紧跟错误的输出）。
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].user == "" {
			continue
		}
		lower := strings.ToLower(turns[i].user)
		for _, sig := range correctionSignals {
			if strings.Contains(lower, sig) {
				return true, turns[i].ts
			}
		}
		return false, ""
	}
	return false, ""
}

type shortTermTurn struct {
	ts   string
	user string
}

// splitShortTermTurns 解析 short-term 的 "## <ts>" 块与 "**User:**" 行。
func splitShortTermTurns(body string) []shortTermTurn {
	var turns []shortTermTurn
	lines := strings.Split(body, "\n")
	var cur *shortTermTurn
	inUser := false
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "## ") {
			ts := strings.TrimSpace(strings.TrimPrefix(t, "## "))
			turns = append(turns, shortTermTurn{ts: ts})
			cur = &turns[len(turns)-1]
			inUser = false
			continue
		}
		if cur == nil {
			continue
		}
		switch {
		case strings.HasPrefix(t, "**User:**"):
			cur.user = strings.TrimSpace(strings.TrimPrefix(t, "**User:**"))
			inUser = true
		case strings.HasPrefix(t, "**Assistant:**"), strings.HasPrefix(t, "**") && strings.Contains(t, "ended**"):
			inUser = false
		case inUser && t != "":
			cur.user += " " + t
		}
	}
	return turns
}
