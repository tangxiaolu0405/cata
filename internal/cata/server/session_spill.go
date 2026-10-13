package server

import (
	"log"
	"strings"

	"cata/internal/cata/brain"
	"cata/internal/llm"
)

// 会话历史「工具结果摘要化 + 溢出」。
//
// 目标：多轮工具循环里，超过阈值轮次的 tool 结果与老 assistant 长正文不再全文注入，
// 压成一行摘要并把原文落到产出区 `.cata/session-spill/<sid>/`，摘要带相对产出区路径，
// 模型可用 read_file 展开。近期轮次保持全文，模型能直接看到最近工具结果原貌。
//
// 实现为「就地改写 history」：一旦某条消息被摘要化，其 Content 被替换为摘要，
// 原文不再回填（幂等——同一消息不会重复溢出）。溢出文件在会话结束时删除。

const (
	// spillKeepRecentRounds 保留全文的最近轮数：距末尾 ≤ 该值的 tool/assistant 消息不动。
	spillKeepRecentRounds = 5
	// spillMinBytes 小于该字节数的正文不值得溢出（摘要化省不了多少 token）。
	spillMinBytes = 1200
	// spillAssistantMinBytes 老 assistant 正文的溢出下限（比 tool 更保守，避免丢对话细节）。
	spillAssistantMinBytes = 2400
)

// spillState 记录本会话已溢出的序号，保证文件名稳定且不重复。
type spillState struct {
	seq int
	// done 已摘要化的消息指纹（避免对同一消息重复溢出）。
	done map[string]bool
}

func newSpillState() *spillState {
	return &spillState{done: make(map[string]bool)}
}

// compactHistoryForContext 就地摘要化 history 中「超过 spillKeepRecentRounds 轮」的
// tool 结果与老 assistant 长正文。outputCwd/sessionID 用于定位溢出文件。
// 返回被摘要化的消息数（0 表示无改动）。
func compactHistoryForContext(history []llm.Message, outputCwd, sessionID string, st *spillState) int {
	if len(history) == 0 || st == nil {
		return 0
	}
	// 以「user 消息」为轮次边界：找出最近 spillKeepRecentRounds 个 user 消息的起点，
	// 该点之后的消息全部保持全文。
	cut := recentRoundsStart(history, spillKeepRecentRounds)
	if cut <= 0 {
		return 0
	}
	changed := 0
	for i := 0; i < cut; i++ {
		m := &history[i]
		switch m.Role {
		case "tool":
			if compactToolMessage(m, outputCwd, sessionID, st) {
				changed++
			}
		case "assistant":
			if compactAssistantMessage(m, outputCwd, sessionID, st) {
				changed++
			}
		}
	}
	return changed
}

// recentRoundsStart 返回「最近 keep 轮」的起始下标：从后往前数第 keep 个 user 消息的位置。
// 不足 keep 轮时返回 0（全部保留）。
func recentRoundsStart(history []llm.Message, keep int) int {
	if keep <= 0 {
		return len(history)
	}
	seen := 0
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != "user" {
			continue
		}
		seen++
		if seen == keep {
			return i
		}
	}
	return 0
}

func compactToolMessage(m *llm.Message, outputCwd, sessionID string, st *spillState) bool {
	body := m.Content
	if len(body) < spillMinBytes {
		return false
	}
	if isAlreadySpilled(body) {
		return false
	}
	key := "tool:" + m.ToolCallID
	if m.ToolCallID == "" {
		key = "tool:" + fingerprint(body)
	}
	if st.done[key] {
		return false
	}
	st.seq++
	fileName := brain.SpillFileName(st.seq, m.Name)
	rel, err := brain.WriteSessionSpill(outputCwd, sessionID, fileName, body)
	if err != nil {
		log.Printf("session spill (tool %s): %v", m.Name, err)
		return false
	}
	m.Content = brain.SpillSummary(rel, body, len(body))
	st.done[key] = true
	return true
}

func compactAssistantMessage(m *llm.Message, outputCwd, sessionID string, st *spillState) bool {
	// 带 tool_calls 的 assistant 消息必须原样保留（下一轮要回传 tool_calls 协议）。
	if len(m.ToolCalls) > 0 {
		return false
	}
	body := m.Content
	if len(body) < spillAssistantMinBytes {
		return false
	}
	if isAlreadySpilled(body) {
		return false
	}
	key := "assistant:" + fingerprint(body)
	if st.done[key] {
		return false
	}
	st.seq++
	fileName := brain.SpillFileName(st.seq, "assistant")
	rel, err := brain.WriteSessionSpill(outputCwd, sessionID, fileName, body)
	if err != nil {
		log.Printf("session spill (assistant): %v", err)
		return false
	}
	m.Content = brain.SpillSummary(rel, body, len(body))
	st.done[key] = true
	return true
}

// isAlreadySpilled 判断正文是否已是摘要替身（幂等保护）。
func isAlreadySpilled(content string) bool {
	return strings.HasPrefix(strings.TrimSpace(content), "[已摘要] 原文 ")
}

// fingerprint 取正文前 64 字节做去重键（避免对同一消息重复溢出）。
func fingerprint(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

// clearSessionSpill 删除某会话的工具结果溢出目录（会话结束清理）。
// outCwd 必须是写入溢出时用的产出区（req.Cwd），与 ws.RootPath（focus_path）可能不同。
// 失败只记日志不阻断。
func (ss *SocketServer) clearSessionSpill(outCwd, sessionID string) {
	out := strings.TrimSpace(outCwd)
	if out == "" {
		out = brain.OutputCwd()
	}
	if out == "" {
		return
	}
	if err := brain.ClearSessionSpill(out, sessionID); err != nil {
		log.Printf("session spill cleanup: %v", err)
	}
}
