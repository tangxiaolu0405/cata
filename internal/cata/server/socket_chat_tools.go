package server

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"cata/internal/cata/brain"
	"cata/internal/llm"
	"cata/internal/mcp"
)

// parallelChatToolNames 可并行执行：只读或异步委派，不占用 exec_confirm / user_choice / browser 会话。
var parallelChatToolNames = map[string]bool{
	"read_file":     true,
	"read_skill":    true,
	"list_files":    true,
	"delegate_task": true,
}

func chatToolParallelSafe(name string) bool {
	return parallelChatToolNames[name]
}

type chatToolBatch struct {
	parallel bool
	calls    []llm.ToolCall
}

// partitionChatToolBatches 将同一轮 tool_calls 切成「可并行段」与「须串行段」。
func partitionChatToolBatches(calls []llm.ToolCall) []chatToolBatch {
	if len(calls) == 0 {
		return nil
	}
	var batches []chatToolBatch
	var cur []llm.ToolCall
	curParallel := false

	flush := func() {
		if len(cur) == 0 {
			return
		}
		batches = append(batches, chatToolBatch{parallel: curParallel, calls: cur})
		cur = nil
	}

	for _, tc := range calls {
		safe := chatToolParallelSafe(tc.Function.Name)
		if len(cur) == 0 {
			curParallel = safe
			cur = append(cur, tc)
			continue
		}
		if safe && curParallel {
			cur = append(cur, tc)
			continue
		}
		flush()
		curParallel = safe
		cur = append(cur, tc)
	}
	flush()
	return batches
}

type chatToolExecResult struct {
	tc   llm.ToolCall
	out  string
	name string
	diff string // 文件写入工具成功时生成的 unified diff（供 file_written 事件展示）
}

// Tool display verbosity levels (agents.md §分级显示).
const (
	displaySilent  = "silent"
	displayNormal  = "normal"
	displayVerbose = "verbose"
)

// toolDisplayLevelForStart 预测工具开始时的显示级别（仅凭工具名，结果未知）。
// 出错时由 tool_result 实际判定为 verbose。
func toolDisplayLevelForStart(name string) string {
	if displayLevelByName(name) == displaySilent {
		return displaySilent
	}
	return displayNormal
}

// toolResultLevel 依据工具名与结果判定最终显示级别：
// read/list 成功 → silent；可逆/常规编辑 → normal；命令执行、出错 → verbose。
func toolResultLevel(name string, out string, err error) string {
	if err != nil || isToolErrorOutput(out) {
		return displayVerbose
	}
	switch name {
	case "read_file", "read_skill", "list_files", "list_modes", "list_playbooks", "playbook_status":
		return displaySilent
	case "search_replace", "append_file", "create_file", "run_skill", "declare_task", "case_artifact", "advance_playbook", "start_playbook", "delegate_task", "delegate_mode", "delegate_wait", "ask_user", "manage_mcp":
		return displayNormal
	default:
		return displayVerbose
	}
}

func displayLevelByName(name string) string {
	switch name {
	case "read_file", "read_skill", "list_files", "list_modes", "list_playbooks", "playbook_status":
		return displaySilent
	case "search_replace", "append_file", "create_file", "run_skill", "declare_task", "case_artifact", "advance_playbook", "start_playbook", "delegate_task", "delegate_mode", "delegate_wait", "ask_user", "manage_mcp":
		return displayNormal
	default:
		return displayVerbose
	}
}

func isToolErrorOutput(out string) bool {
	s := strings.TrimSpace(out)
	if s == "" {
		return false
	}
	// 常见错误前缀；保守判定，避免把正常输出误判为 verbose。
	prefixes := []string{"[error]", "Error:", "error:", "failed:", "exec: ", "exit status"}
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// executeChatToolCalls runs one model round's tool_calls (parallel batches where safe).
func (ss *SocketServer) executeChatToolCalls(
	ctx context.Context,
	conn net.Conn,
	client *llm.Client,
	history *[]llm.Message,
	tools []llm.Tool,
	toolCalls []llm.ToolCall,
	round int,
	sessPromptTok, sessCompletionTok *int,
	chatWS *brain.Workspace,
) ([]chatToolExecResult, error) {
	var all []chatToolExecResult
	fatalBrowser := false
	for _, batch := range partitionChatToolBatches(toolCalls) {
		if ctx.Err() != nil {
			return all, ctx.Err()
		}
		if batch.parallel && len(batch.calls) > 1 {
			batchRes, err := ss.runChatToolBatchParallel(ctx, conn, client, history, tools, batch.calls, round, sessPromptTok, sessCompletionTok, chatWS)
			all = append(all, batchRes...)
			if err != nil {
				return all, err
			}
			continue
		}
		for _, tc := range batch.calls {
			if ctx.Err() != nil {
				return all, ctx.Err()
			}
			res, fb, err := ss.runChatToolSequential(ctx, conn, client, history, tools, tc, round, sessPromptTok, sessCompletionTok, chatWS, fatalBrowser)
			if err != nil {
				return all, err
			}
			fatalBrowser = fb
			ss.appendChatToolResult(history, res)
			all = append(all, res)
		}
	}
	return all, nil
}

func (ss *SocketServer) runChatToolSequential(
	ctx context.Context,
	conn net.Conn,
	client *llm.Client,
	history *[]llm.Message,
	tools []llm.Tool,
	tc llm.ToolCall,
	round int,
	sessPromptTok, sessCompletionTok *int,
	chatWS *brain.Workspace,
	fatalBrowser bool,
) (chatToolExecResult, bool, error) {
	name := tc.Function.Name
	cc := brain.ChatContextFrom(ctx)
	ss.emitChatStats(ctx, conn, client, history, tools, round, llm.StreamUsage{}, sessPromptTok, sessCompletionTok, cc.Profile, cc.OutputCwd, name, chatWS, subagentRunningFrom(ctx))
	_ = ss.emitStreamLine(conn, map[string]interface{}{"type": "tool_start", "id": tc.ID, "name": name, "level": toolDisplayLevelForStart(name)})

	var out string
	var terr error
	if fatalBrowser && mcp.IsBrowserTool(name) {
		out = "[browser error] skipped: browser crashed (see previous error)"
	} else {
		out, terr = ss.runTerminalTool(ctx, conn, tc)
	}
	out = mergeToolOutputError(out, terr)
	if !fatalBrowser && isFatalBrowserError(terr, out) {
		fatalBrowser = true
	}
	_ = ss.emitStreamLine(conn, map[string]interface{}{"type": "tool_result", "id": tc.ID, "name": name, "output": out, "level": toolResultLevel(name, out, terr)})
	return chatToolExecResult{tc: tc, out: out, name: name}, fatalBrowser, nil
}

func (ss *SocketServer) runChatToolBatchParallel(
	ctx context.Context,
	conn net.Conn,
	client *llm.Client,
	history *[]llm.Message,
	tools []llm.Tool,
	calls []llm.ToolCall,
	round int,
	sessPromptTok, sessCompletionTok *int,
	chatWS *brain.Workspace,
) ([]chatToolExecResult, error) {
	_ = ss.emitStreamLine(conn, map[string]interface{}{
		"type":    "progress",
		"message": fmt.Sprintf("executing %d tools in parallel", len(calls)),
	})

	results := make([]chatToolExecResult, len(calls))
	var wg sync.WaitGroup
	errCh := make(chan error, 1)

	for i, tc := range calls {
		wg.Add(1)
		go func(i int, tc llm.ToolCall) {
			defer wg.Done()
			if ctx.Err() != nil {
				select {
				case errCh <- ctx.Err():
				default:
				}
				return
			}
			name := tc.Function.Name
			cc := brain.ChatContextFrom(ctx)
			ss.emitChatStats(ctx, conn, client, history, tools, round, llm.StreamUsage{}, sessPromptTok, sessCompletionTok, cc.Profile, cc.OutputCwd, name, chatWS, subagentRunningFrom(ctx))
			_ = ss.emitStreamLine(conn, map[string]interface{}{"type": "tool_start", "id": tc.ID, "name": name, "level": toolDisplayLevelForStart(name)})
			// 每次工具执行用独立 ctx + diff sink：写入工具成功时把 unified diff 填进来，
			// 供 file_written 事件展示（并行执行时互不串扰）。
			var diff string
			tctx := withFileDiffSink(ctx, &diff)
			out, terr := ss.runTerminalTool(tctx, conn, tc)
			results[i] = chatToolExecResult{tc: tc, out: mergeToolOutputError(out, terr), name: name, diff: diff}
		}(i, tc)
	}

	wg.Wait()
	select {
	case err := <-errCh:
		return results, err
	default:
	}

	for _, res := range results {
		_ = ss.emitStreamLine(conn, map[string]interface{}{
			"type": "tool_result", "id": res.tc.ID, "name": res.name, "output": res.out,
			"level": toolResultLevel(res.name, res.out, nil),
		})
		// 文件写入成功（无 error 标记）时附带 file_written 事件，TUI 可展示落盘路径。
		ss.maybeEmitFileWritten(conn, res)
		ss.appendChatToolResult(history, res)
	}
	return results, nil
}

// fileWriteTools 产生落盘结果的文件写入工具（成功 out 里带 resolved= 绝对路径）。
var fileWriteTools = map[string]bool{
	"create_file":    true,
	"append_file":    true,
	"search_replace": true,
}

// maybeEmitFileWritten 对成功的文件写入工具结果发 file_written 事件（path/bytes/op）。
// 解析复用 out 里已含的 resolved= 路径与字节数，不重复 stat。
func (ss *SocketServer) maybeEmitFileWritten(conn net.Conn, res chatToolExecResult) {
	if !fileWriteTools[res.name] {
		return
	}
	if strings.Contains(res.out, "[error]") || strings.Contains(strings.ToLower(res.out), "error:") {
		return
	}
	path := extractResolvedPath(res.out)
	if path == "" {
		return
	}
	ev := map[string]interface{}{
		"type": "file_written",
		"name": res.name,
		"path": path,
		"id":   res.tc.ID,
	}
	// 尽量从 out 提取字节数（"wrote N bytes" / "appended N bytes" / "N -> M bytes"）。
	if n, ok := extractWrittenBytes(res.out); ok {
		ev["bytes"] = n
	}
	// 写入工具生成的 unified diff：TUI verbose 模式展示改动内容。
	if res.diff != "" {
		ev["diff"] = res.diff
	}
	_ = ss.emitStreamLine(conn, ev)
}

// extractResolvedPath 从工具结果文本提取 resolved= 路径。
func extractResolvedPath(out string) string {
	const marker = "resolved="
	i := strings.Index(out, marker)
	if i < 0 {
		return ""
	}
	rest := out[i+len(marker):]
	// 路径到下一个冒号/空格（create_file a.txt resolved=/abs/a.txt: wrote 12 bytes）。
	for j := 0; j < len(rest); j++ {
		switch rest[j] {
		case ':', ' ', '\n':
			return rest[:j]
		}
	}
	return rest
}

// extractWrittenBytes 从工具结果提取写入字节数（wrote N / appended N / N -> M）。
func extractWrittenBytes(out string) (int, bool) {
	for _, pat := range []string{"wrote ", "appended "} {
		if i := strings.Index(out, pat); i >= 0 {
			num := strings.Fields(out[i+len(pat):])
			if len(num) > 0 {
				if n, err := strconv.Atoi(num[0]); err == nil {
					return n, true
				}
			}
		}
	}
	// search_replace: "N -> M bytes"
	arrow := strings.Index(out, " -> ")
	if arrow >= 0 {
		num := strings.Fields(out[arrow+4:])
		if len(num) > 0 {
			if n, err := strconv.Atoi(num[0]); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

func (ss *SocketServer) appendChatToolResult(history *[]llm.Message, res chatToolExecResult) {
	*history = append(*history, llm.Message{
		Role:       "tool",
		ToolCallID: res.tc.ID,
		Name:       res.name,
		Content:    serverRedactor.Redact(res.out),
	})
}

func mergeToolOutputError(out string, err error) string {
	if err == nil {
		return out
	}
	if out != "" {
		return out + "\n[error] " + err.Error()
	}
	return "[error] " + err.Error()
}
