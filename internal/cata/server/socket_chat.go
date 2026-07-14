package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
	"cata/internal/cata/evolve"
	"cata/internal/llm"
	"cata/internal/mcp"
)

var activeChatStreams int32

// 压缩后 socket history 目标占用（相对 context_window 的比例，为回复与 tool 留空）。
const historyBudgetAfterCompressRatio = 0.40

// 终端对话：history 仅维护 user / assistant / tool。boot-leader.md 与 brain 节选由 internal/llm.Client.withBootLeaderSystemMessage 在出站前注入为前两条 system（与 user 无关）；工具仅经 API 的 tools 字段。旧版 terminalUserContent 已移除。

// emitStreamLine 向 CLI 写入一行 NDJSON（无换行外的分隔；每条独立 JSON）。
func (ss *SocketServer) emitStreamLine(conn net.Conn, ev map[string]interface{}) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = conn.Write(data)
	return err
}

// handleTerminalChatStream 流式 + 服务端工具循环；协议为多条 NDJSON，最后一条 type=done。
// chatWS 为本轮 chat 解析出的脑子分区（勿用 brain.Active()，后台 evolve 会临时改写全局 Active）。
// promptPeak 为本连接会话内已达最高 prompt 档位（sticky，chat_reset 清零）。
func (ss *SocketServer) handleTerminalChatStream(conn net.Conn, br *bufio.Reader, history *[]llm.Message, userText string, chatWS *brain.Workspace, promptPeak *brain.PromptProfile) (err error) {
	atomic.AddInt32(&activeChatStreams, 1)
	defer atomic.AddInt32(&activeChatStreams, -1)
	var lr *connLineReader
	defer func() {
		if r := recover(); r != nil {
			log.Printf("chat stream panic: %v\n%s", r, debug.Stack())
			if lr != nil {
				lr.Stop()
			}
			_ = ss.emitStreamLine(conn, map[string]interface{}{
				"type": "error", "message": fmt.Sprintf("internal error: %v", r),
			})
			_ = ss.emitStreamLine(conn, map[string]interface{}{"type": "done", "success": false})
			err = fmt.Errorf("panic: %v", r)
		}
	}()

	_ = config.InitBrainPath()

	text := strings.TrimSpace(userText)
	if text == "" {
		_ = ss.emitStreamLine(conn, map[string]interface{}{"type": "error", "message": "empty message"})
		_ = ss.emitStreamLine(conn, map[string]interface{}{"type": "done", "success": false})
		return fmt.Errorf("empty message")
	}

	client, err := llm.NewClientForRole(llm.RoleChat)
	if err != nil {
		_ = ss.emitStreamLine(conn, map[string]interface{}{"type": "error", "message": fmt.Sprintf("LLM: %v", err)})
		_ = ss.emitStreamLine(conn, map[string]interface{}{"type": "done", "success": false})
		return err
	}

	*history = append(*history, llm.Message{Role: "user", Content: text})

	if len(ss.buildTerminalChatToolsForTier(ToolTierLight)) == 0 {
		msg := "无可用工具：请在 " + config.GetConfigPath() + " 启用 exec.enabled 或 workspace_files.enabled，然后 /exit 重进以拉起新 server。"
		_ = ss.emitStreamLine(conn, map[string]interface{}{"type": "error", "message": msg})
		_ = ss.emitStreamLine(conn, map[string]interface{}{"type": "done", "success": false})
		return fmt.Errorf("no terminal tools enabled")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lr = newConnLineReader(br, conn, cancel)
	ctx = withChatConnReader(ctx, lr)
	pool := newSubagentPool(ctx, ss, conn)
	ctx = withChatSubagentPool(ctx, pool)

	var sessPromptTok, sessCompletionTok int

	for round := 1; ; round++ {
		if ctx.Err() != nil {
			brain.ClearPromptProfile()
			return ss.emitChatCancelled(conn, lr)
		}
		tier := InferToolTier(round, *history, text)
		roundProfile := PromptProfileForTier(tier)
		if promptPeak != nil {
			*promptPeak = brain.PromptProfileMax(*promptPeak, roundProfile)
			brain.SetPromptProfile(*promptPeak)
		} else {
			brain.SetPromptProfile(roundProfile)
		}
		tools := ss.buildTerminalChatToolsForTier(tier)
		ss.maybeContextCompress(conn, client, history, tools)
		_ = ss.emitStreamLine(conn, map[string]interface{}{"type": "progress", "message": fmt.Sprintf("model round %d", round)})

		onDelta := func(s string) error {
			if s == "" {
				return nil
			}
			return ss.emitStreamLine(conn, map[string]interface{}{"type": "token", "content": s})
		}

		const maxLLMAttempts = 3
		var asst string
		var reasoning string
		var toolCalls []llm.ToolCall
		var finishReason string
		var roundUsage llm.StreamUsage
		var err error
		for attempt := 1; attempt <= maxLLMAttempts; attempt++ {
			if attempt > 1 {
				_ = ss.emitStreamLine(conn, map[string]interface{}{
					"type": "progress", "message": fmt.Sprintf("LLM 超时或网络抖动，重试 %d/%d …", attempt, maxLLMAttempts),
				})
				time.Sleep(time.Duration(attempt) * time.Second)
			}
			asst, reasoning, toolCalls, finishReason, roundUsage, err = client.ChatStreamRound(ctx, *history, tools, "auto", 0, 0, onDelta)
			toolCalls = llm.NormalizeToolCalls(toolCalls)
			if err == nil {
				break
			}
			if !llm.IsRetryableChatError(err) || attempt == maxLLMAttempts {
				break
			}
			log.Printf("chat stream round %d attempt %d: %v", round, attempt, err)
		}
		ss.emitChatStats(conn, client, history, tools, round, roundUsage, &sessPromptTok, &sessCompletionTok, "", chatWS, subagentRunningFrom(ctx))
		if strings.EqualFold(finishReason, "length") {
			_ = ss.emitStreamLine(conn, map[string]interface{}{
				"type":    "error",
				"message": "模型输出已达 max_tokens 上限，回复可能被截断；可在 ~/.cata/config.json 提高 llm.max_tokens。",
			})
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				brain.ClearPromptProfile()
				return ss.emitChatCancelled(conn, lr)
			}
			msg := err.Error() + "\n\n本连接对话上下文已保留（含已执行的工具结果）。直接输入「继续」即可接着做，无需从头重述任务。"
			lr.Stop()
			_ = ss.emitStreamLine(conn, map[string]interface{}{"type": "error", "message": msg})
			_ = ss.emitStreamLine(conn, map[string]interface{}{"type": "done", "success": false})
			brain.ClearPromptProfile()
			return err
		}

		if len(toolCalls) == 0 {
			if parsed, stripped := llm.ParseEmbeddedToolCalls(asst); len(parsed) > 0 {
				toolCalls = llm.NormalizeToolCalls(parsed)
				asst = stripped
				_ = ss.emitStreamLine(conn, map[string]interface{}{
					"type": "progress", "message": fmt.Sprintf("executing %d tool(s) from model output", len(parsed)),
				})
			} else if strings.Contains(strings.ToLower(asst), "<tool") || strings.Contains(asst, "[tool_call") {
				hint := "模型返回了 tool 标记但未解析成功；大文件请分块 append_file。/exit 后重进以加载新 server。"
				log.Printf("embedded tool parse failed, content prefix: %.200q", asst)
				_ = ss.emitStreamLine(conn, map[string]interface{}{"type": "error", "message": hint})
			}
		} else if len(toolCalls) > 0 {
			// 流式 arguments 可能截断；尝试从正文中的 [tool_call name] {json} 补全
			if parsed, stripped := llm.ParseEmbeddedToolCalls(asst); len(parsed) > 0 {
				byName := make(map[string]llm.ToolCall)
				for _, p := range parsed {
					if llm.NormalizeToolArguments(p.Function.Name, p.Function.Arguments) != "" {
						byName[p.Function.Name] = p
					}
				}
				for i := range toolCalls {
					if llm.NormalizeToolArguments(toolCalls[i].Function.Name, toolCalls[i].Function.Arguments) != "" {
						continue
					}
					if p, ok := byName[toolCalls[i].Function.Name]; ok {
						streamID := toolCalls[i].ID
						toolCalls[i] = p
						if streamID != "" {
							toolCalls[i].ID = streamID
						}
					}
				}
				toolCalls = llm.NormalizeToolCalls(toolCalls)
				asst = stripped
			}
		}

		if len(toolCalls) == 0 {
			*history = append(*history, llm.Message{Role: "assistant", Content: asst})
			if err := brain.AppendChatTurn(text, asst); err != nil {
				log.Printf("short-term memory: %v", err)
			}
			ss.maybeContextCompress(conn, client, history, tools)
			brain.ClearPromptProfile()
			return ss.emitChatDone(conn, lr, true, false)
		}

		*history = append(*history, llm.Message{
			Role:             "assistant",
			Content:          asst,
			ReasoningContent: reasoning,
			ToolCalls:        toolCalls,
		})

		if err := ss.executeChatToolCalls(ctx, conn, client, history, tools, toolCalls, round, &sessPromptTok, &sessCompletionTok, chatWS); err != nil {
			if errors.Is(err, context.Canceled) {
				brain.ClearPromptProfile()
				return ss.emitChatCancelled(conn, lr)
			}
			brain.ClearPromptProfile()
			return err
		}
		brain.ClearPromptProfile()
	}
}

func (ss *SocketServer) emitChatDone(conn net.Conn, lr *connLineReader, success, cancelled bool) error {
	if lr != nil {
		lr.Stop()
	}
	ev := map[string]interface{}{"type": "done", "success": success}
	if cancelled {
		ev["cancelled"] = true
	}
	return ss.emitStreamLine(conn, ev)
}

func (ss *SocketServer) emitChatCancelled(conn net.Conn, lr *connLineReader) error {
	if lr != nil {
		lr.Stop()
	}
	_ = ss.emitStreamLine(conn, map[string]interface{}{"type": "progress", "message": "已停止"})
	return ss.emitStreamLine(conn, map[string]interface{}{"type": "done", "success": false, "cancelled": true})
}

// maybeContextCompress 当估算输入 token ≥ context_window×ratio（默认 85%）时，触发自主演进压缩并裁短 socket history。
// history 指本连接内存中的多轮 user/assistant/tool，不是 short-term 文件；short-term 由 AppendChatTurn 写入磁盘供 evolve 提炼。
func (ss *SocketServer) maybeContextCompress(conn net.Conn, client *llm.Client, history *[]llm.Message, tools []llm.Tool) {
	if config.Config == nil || !config.Config.Evolution.Enabled {
		return
	}
	window := client.ContextWindowTokens()
	threshold := llm.ContextCompressThreshold(window)
	est := client.EstimatedChatInputTokens(*history, tools)
	if est < threshold {
		return
	}
	_ = ss.emitStreamLine(conn, map[string]interface{}{
		"type":    "progress",
		"message": fmt.Sprintf("context ~%d/%d tokens (≥%.0f%%), consolidating memory...", est, window, llm.ContextCompressRatioValue()*100),
	})
	if err := evolve.RunSessionCompress(context.Background()); err != nil {
		log.Printf("session compress: %v", err)
		return
	}
	budget := int(float64(window) * historyBudgetAfterCompressRatio)
	*history = trimHistoryToTokenBudget(client, *history, tools, budget)
}

func (ss *SocketServer) buildTerminalChatTools() []llm.Tool {
	key := ss.chatToolsCacheKey()
	if key == ss.chatToolsKey && len(ss.chatToolsCache) > 0 {
		out := make([]llm.Tool, len(ss.chatToolsCache))
		copy(out, ss.chatToolsCache)
		return out
	}
	_ = config.InitBrainPath()
	out := ss.tools.Schemas()
	if mgr := mcp.Global(); mgr != nil {
		out = append(out, mgr.Tools()...)
	}
	ss.chatToolsKey = key
	ss.chatToolsCache = out
	return out
}

func (ss *SocketServer) chatToolsCacheKey() string {
	var b strings.Builder
	b.WriteString(mcp.ActiveCapsKey())
	if cfg := config.Config; cfg != nil {
		b.WriteString("|exec:")
		b.WriteString(strconv.FormatBool(cfg.Exec.Enabled))
		b.WriteString("|files:")
		b.WriteString(strconv.FormatBool(cfg.WorkspaceFilesEnabled()))
		b.WriteString("|mcp:")
		b.WriteString(strconv.FormatBool(cfg.MCP.Enabled))
	}
	b.WriteString("|n:")
	b.WriteString(strconv.Itoa(len(ss.tools.Names())))
	return b.String()
}

func (ss *SocketServer) runTerminalTool(ctx context.Context, conn net.Conn, tc llm.ToolCall) (string, error) {
	fn := tc.Function
	name := fn.Name
	argsJSON := llm.NormalizeToolArguments(name, strings.TrimSpace(fn.Arguments))
	if argsJSON == "" {
		argsJSON = "{}"
	}

	if mgr := mcp.Global(); mgr != nil {
		if out, err, ok := mgr.TryCall(ctx, name, argsJSON); ok {
			return out, err
		}
	}

	return ss.tools.Dispatch(ctx, conn, name, argsJSON)
}

// formatCommandResult builds a structured string for the LLM from command execution results.
// Always includes cwd, command, and exit code. Includes stdout/stderr separated when relevant.
func formatCommandResult(wd, cmdLine string, exitCode int, timedOut, truncated bool, stdoutStr, stderrStr string) string {
	var b strings.Builder
	b.WriteString("[run_command]\n")
	b.WriteString("cwd: ")
	b.WriteString(wd)
	b.WriteString("\n$ ")
	b.WriteString(cmdLine)

	if timedOut {
		b.WriteString("\nexit: timeout")
	} else {
		b.WriteString(fmt.Sprintf("\nexit: %d", exitCode))
	}
	if truncated {
		b.WriteString(" (output truncated)")
	}
	b.WriteString("\n")

	hasOut := len(strings.TrimSpace(stdoutStr)) > 0
	hasErr := len(strings.TrimSpace(stderrStr)) > 0

	if hasOut && hasErr {
		b.WriteString("--- stdout ---\n")
		b.WriteString(stdoutStr)
		if !strings.HasSuffix(stdoutStr, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("--- stderr ---\n")
		b.WriteString(stderrStr)
	} else if hasErr {
		b.WriteString("--- stderr ---\n")
		b.WriteString(stderrStr)
	} else if hasOut {
		b.WriteString(stdoutStr)
	}
	return b.String()
}

// truncateCmdOutput keeps the head and tail of combined stdout+stderr when total exceeds maxBytes.
// Preserves roughly headRatio of output from the start and (1-headRatio) from the end.
func truncateCmdOutput(stdoutStr, stderrStr string, maxBytes int) (string, string) {
	const headRatio = 0.25
	headBudget := int(float64(maxBytes) * headRatio)
	tailBudget := maxBytes - headBudget - 200 // reserve 200 bytes for the truncation notice

	total := stdoutStr + stderrStr
	if len(total) <= maxBytes {
		return stdoutStr, stderrStr
	}

	// Build combined truncated output
	notice := fmt.Sprintf("\n... (truncated %d bytes) ...\n", len(total)-maxBytes)

	// Take head from beginning, tail from end
	head := safeSlice(total, 0, headBudget)
	tail := safeSlice(total, len(total)-tailBudget, len(total))

	return "", head + notice + tail
}

func safeSlice(s string, start, end int) string {
	if start < 0 {
		start = 0
	}
	if end > len(s) {
		end = len(s)
	}
	if start >= end {
		return ""
	}
	// Try not to break UTF-8; walk back to a valid boundary
	for start > 0 && start < len(s) && s[start]&0xC0 == 0x80 {
		start--
	}
	for end > start && end < len(s) && s[end]&0xC0 == 0x80 {
		end--
	}
	return s[start:end]
}

// safePathUnder 将 rel 限制在 base 目录之下。
func safePathUnder(base, rel string) (string, error) {
	return brain.PathUnderBase(base, rel)
}

func resolveExecCwd() (string, error) {
	return brain.ExecWorkingDir()
}

// isFatalBrowserError returns true when the tool error or output indicates
// the browser process died — remaining browser tool calls in this round are futile.
func isFatalBrowserError(err error, output string) bool {
	if err == nil && !strings.Contains(output, "[browser error]") {
		return false
	}
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "broken pipe") ||
			strings.Contains(msg, "eof") ||
			strings.Contains(msg, "process already finished") {
			return true
		}
	}
	return strings.Contains(output, "[browser error]") &&
		(strings.Contains(output, "Target closed") ||
			strings.Contains(output, "Browser closed") ||
			strings.Contains(output, "Protocol error") ||
			strings.Contains(output, "has been closed"))
}

// trimHistoryToTokenBudget 从最早的用户/助手/tool 消息裁掉，使估算 token ≤ budget。
func trimHistoryToTokenBudget(client *llm.Client, msgs []llm.Message, tools []llm.Tool, budget int) []llm.Message {
	if budget <= 0 || len(msgs) == 0 {
		return msgs
	}
	out := append([]llm.Message(nil), msgs...)
	for len(out) > 1 && client.EstimatedChatInputTokens(out, tools) > budget {
		drop := firstDroppableIndex(out)
		if drop < 0 {
			break
		}
		out = append(out[:drop], out[drop+1:]...)
	}
	return out
}

func firstDroppableIndex(msgs []llm.Message) int {
	for i, m := range msgs {
		switch m.Role {
		case "user", "assistant", "tool":
			return i
		}
	}
	return -1
}

func (ss *SocketServer) emitChatStats(conn net.Conn, client *llm.Client, history *[]llm.Message, tools []llm.Tool, round int, usage llm.StreamUsage, sessIn, sessOut *int, lastTool string, chatWS *brain.Workspace, subagentRunning int) {
	in := usage.PromptTokens
	out := usage.CompletionTokens
	if in == 0 && out == 0 && usage.TotalTokens > 0 {
		in = usage.TotalTokens
	}
	if in > 0 {
		*sessIn += in
	}
	if out > 0 {
		*sessOut += out
	}
	ctxEst := client.EstimatedChatInputTokens(*history, tools)
	ev := map[string]interface{}{
		"type":               "stats",
		"round":              round,
		"model":              client.ModelName(),
		"model_role":         "chat",
		"prompt_tokens":      in,
		"completion_tokens":  out,
		"session_prompt":     *sessIn,
		"session_completion": *sessOut,
		"context_est":        ctxEst,
		"context_window":     client.ContextWindowTokens(),
		"tools":              len(tools),
		"last_tool":          lastTool,
		"prompt_profile":     string(brain.ActivePromptProfile()),
	}
	w := chatWS
	if w == nil {
		w = brain.Active()
	}
	if w != nil {
		ev["workspace_id"] = w.ID
		ev["focus_path"] = w.RootPath
		ev["active_mode"] = w.ActiveMode
	}
	if outCwd := brain.OutputCwd(); outCwd != "" {
		ev["output_cwd"] = outCwd
	}
	if subagentRunning > 0 {
		ev["subagent_running"] = subagentRunning
		if cfg := config.Config; cfg != nil && cfg.Subagent.MaxConcurrent > 0 {
			ev["subagent_max"] = cfg.Subagent.MaxConcurrent
		}
	}
	_ = ss.emitStreamLine(conn, ev)
}
