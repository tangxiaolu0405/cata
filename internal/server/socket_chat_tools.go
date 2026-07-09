package server

import (
	"context"
	"fmt"
	"net"
	"sync"

	"cata/internal/brain"
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
) error {
	fatalBrowser := false
	for _, batch := range partitionChatToolBatches(toolCalls) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if batch.parallel && len(batch.calls) > 1 {
			if err := ss.runChatToolBatchParallel(ctx, conn, client, history, tools, batch.calls, round, sessPromptTok, sessCompletionTok, chatWS); err != nil {
				return err
			}
			continue
		}
		for _, tc := range batch.calls {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			res, fb, err := ss.runChatToolSequential(ctx, conn, client, history, tools, tc, round, sessPromptTok, sessCompletionTok, chatWS, fatalBrowser)
			if err != nil {
				return err
			}
			fatalBrowser = fb
			ss.appendChatToolResult(history, res)
		}
	}
	return nil
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
	ss.emitChatStats(conn, client, history, tools, round, llm.StreamUsage{}, sessPromptTok, sessCompletionTok, name, chatWS, subagentRunningFrom(ctx))
	_ = ss.emitStreamLine(conn, map[string]interface{}{"type": "tool_start", "id": tc.ID, "name": name})

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
	_ = ss.emitStreamLine(conn, map[string]interface{}{"type": "tool_result", "id": tc.ID, "name": name, "output": out})
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
) error {
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
			ss.emitChatStats(conn, client, history, tools, round, llm.StreamUsage{}, sessPromptTok, sessCompletionTok, name, chatWS, subagentRunningFrom(ctx))
			_ = ss.emitStreamLine(conn, map[string]interface{}{"type": "tool_start", "id": tc.ID, "name": name})
			out, terr := ss.runTerminalTool(ctx, conn, tc)
			results[i] = chatToolExecResult{tc: tc, out: mergeToolOutputError(out, terr), name: name}
		}(i, tc)
	}

	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
	}

	for _, res := range results {
		_ = ss.emitStreamLine(conn, map[string]interface{}{
			"type": "tool_result", "id": res.tc.ID, "name": res.name, "output": res.out,
		})
		ss.appendChatToolResult(history, res)
	}
	return nil
}

func (ss *SocketServer) appendChatToolResult(history *[]llm.Message, res chatToolExecResult) {
	*history = append(*history, llm.Message{
		Role:       "tool",
		ToolCallID: res.tc.ID,
		Name:       res.name,
		Content:    res.out,
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
