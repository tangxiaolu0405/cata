package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
)

// Fail codes reported to the client via NDJSON error/done.
const (
	FailCodeBudgetExhausted     = "budget_exhausted"
	FailCodeConsecutiveFailures = "consecutive_failures"
	FailCodeNoProgress          = "no_progress"
)

// chatLoopBreak 主 chat 工具环熔断原因。
type chatLoopBreak struct {
	Code   string
	Reason string
}

func (b *chatLoopBreak) Error() string {
	if b == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", b.Code, b.Reason)
}

// chatLoopGuard 进度检测 + 连续失败 + 轮次预算。
// 限额来自任务契约（declare_task）；未声明的维度不熔断。
// 全局仅保留 hard_max_tool_rounds 作为防失控天花板。
type chatLoopGuard struct {
	maxRounds              int // 0 = 仅 hard ceiling
	maxConsecutiveFailures int // 0 = 关闭
	maxStaleRounds         int // 0 = 关闭
	hardMaxRounds          int

	consecutiveFailures int
	staleRounds         int
	lastFingerprint     string
}

func newChatLoopGuard(task *brain.TaskState) *chatLoopGuard {
	g := &chatLoopGuard{
		hardMaxRounds: config.ChatHardMaxToolRounds(),
	}
	g.applyTask(task)
	return g
}

func (g *chatLoopGuard) applyTask(task *brain.TaskState) {
	if g == nil || task == nil {
		return
	}
	g.maxRounds = task.MaxToolRounds
	g.maxConsecutiveFailures = task.MaxConsecutiveFailures
	g.maxStaleRounds = task.MaxStaleRounds
}

func (g *chatLoopGuard) checkBudget(round int) *chatLoopBreak {
	if g == nil {
		return nil
	}
	limit := g.maxRounds
	if limit <= 0 {
		limit = g.hardMaxRounds
	} else if g.hardMaxRounds > 0 && limit > g.hardMaxRounds {
		limit = g.hardMaxRounds
	}
	if limit > 0 && round > limit {
		src := "task max_tool_rounds"
		if g.maxRounds <= 0 {
			src = "hard ceiling (declare_task max_tool_rounds to set a task-specific budget)"
		}
		return &chatLoopBreak{
			Code:   FailCodeBudgetExhausted,
			Reason: fmt.Sprintf("tool rounds exceeded %s (%d/%d); task failed — say「继续」after adjusting approach or declare_task limits", src, round-1, limit),
		}
	}
	return nil
}

func (g *chatLoopGuard) observe(results []chatToolExecResult) *chatLoopBreak {
	if g == nil || len(results) == 0 {
		return nil
	}
	anyFail := false
	anyOK := false
	var parts []string
	for _, r := range results {
		fail := toolResultLooksFailed(r.out)
		if fail {
			anyFail = true
		} else {
			anyOK = true
		}
		parts = append(parts, fmt.Sprintf("%s:%t:%s", r.name, fail, fingerprintSnippet(r.out)))
	}
	fp := hashFingerprint(strings.Join(parts, "|"))
	if g.lastFingerprint != "" && fp == g.lastFingerprint {
		g.staleRounds++
	} else {
		g.staleRounds = 0
		g.lastFingerprint = fp
	}
	if anyFail && !anyOK {
		g.consecutiveFailures++
	} else if anyOK {
		g.consecutiveFailures = 0
	}

	if g.maxConsecutiveFailures > 0 && g.consecutiveFailures >= g.maxConsecutiveFailures {
		return &chatLoopBreak{
			Code:   FailCodeConsecutiveFailures,
			Reason: fmt.Sprintf("stopped after %d consecutive tool-round failures (task limit %d); last tools: %s", g.consecutiveFailures, g.maxConsecutiveFailures, summarizeToolNames(results)),
		}
	}
	if g.maxStaleRounds > 0 && g.staleRounds >= g.maxStaleRounds {
		return &chatLoopBreak{
			Code:   FailCodeNoProgress,
			Reason: fmt.Sprintf("no progress for %d tool rounds (task limit %d); last tools: %s", g.staleRounds, g.maxStaleRounds, summarizeToolNames(results)),
		}
	}
	return nil
}

func toolResultLooksFailed(out string) bool {
	s := strings.TrimSpace(out)
	if s == "" {
		return true
	}
	low := strings.ToLower(s)
	if strings.Contains(s, "[error]") {
		return true
	}
	if strings.Contains(low, "exit status") || strings.Contains(low, "exit code") {
		return true
	}
	if strings.HasPrefix(low, "error:") || strings.Contains(low, "\nerror:") {
		return true
	}
	if strings.Contains(low, "permission denied") || strings.Contains(low, "timed out") {
		return true
	}
	return false
}

func fingerprintSnippet(out string) string {
	s := strings.TrimSpace(out)
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

func hashFingerprint(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

func summarizeToolNames(results []chatToolExecResult) string {
	names := make([]string, 0, len(results))
	for _, r := range results {
		names = append(names, r.name)
	}
	return strings.Join(names, ", ")
}
