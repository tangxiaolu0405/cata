package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"

	"cata/internal/cata/brain"
)

// ReadOpenAIChatStream 读取 OpenAI 兼容的 text/event-stream（data: JSON 行），
// 将 assistant 文本增量交给 onDelta，并返回合并正文、工具调用与 finish_reason。
func ReadOpenAIChatStream(r io.Reader, onDelta func(string) error, onReasoning func(string) error) (content string, reasoning string, toolCalls []ToolCall, finishReason string, usage StreamUsage, err error) {
	br := bufio.NewReader(r)
	aggs := make(map[int]*streamToolAgg)
	var contentBuf strings.Builder
	var reasoningBuf strings.Builder
	var lastChoiceMessageTools []ToolCall
	var lastChoiceReasoning string

	for {
		rawLine, readErr := br.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return "", "", nil, "", usage, readErr
		}
		line := strings.TrimSpace(strings.TrimSuffix(rawLine, "\r"))
		if line == "" {
			if readErr == io.EOF {
				break
			}
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}

		var wrap struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if e := json.Unmarshal([]byte(payload), &wrap); e == nil && wrap.Error != nil {
			return "", "", nil, "", usage, fmt.Errorf("stream API error: %s", wrap.Error.Message)
		}

		var chunk streamChunk
		if e := json.Unmarshal([]byte(payload), &chunk); e != nil {
			continue
		}
		if chunk.Usage != nil {
			usage = *chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			if readErr == io.EOF {
				break
			}
			continue
		}

		ch := chunk.Choices[0]
		if ch.FinishReason != nil && *ch.FinishReason != "" {
			finishReason = *ch.FinishReason
		}

		d := ch.Delta
		if d.ReasoningContent != "" {
			reasoningBuf.WriteString(d.ReasoningContent)
			if onReasoning != nil {
				if e := onReasoning(d.ReasoningContent); e != nil {
					return "", "", nil, "", usage, e
				}
			}
		}
		if d.Content != "" {
			contentBuf.WriteString(d.Content)
			if onDelta != nil {
				if e := onDelta(d.Content); e != nil {
					return "", "", nil, "", usage, e
				}
			}
		}
		for _, td := range d.ToolCalls {
			mergeToolDelta(aggs, td)
		}

		// 部分兼容实现（含若干网关）在最后一帧带 choices[].message.tool_calls，而非 delta 分片
		if ch.Message != nil {
			if len(ch.Message.ToolCalls) > 0 {
				lastChoiceMessageTools = append([]ToolCall(nil), ch.Message.ToolCalls...)
			}
			if ch.Message.ReasoningContent != "" {
				lastChoiceReasoning = ch.Message.ReasoningContent
			}
			if ch.Message.Content != "" && d.Content == "" {
				contentBuf.WriteString(ch.Message.Content)
				if onDelta != nil {
					if e := onDelta(ch.Message.Content); e != nil {
						return "", "", nil, "", usage, e
					}
				}
			}
		}

		if readErr == io.EOF {
			break
		}
	}

	if len(aggs) > 0 {
		toolCalls = finalizeStreamToolCalls(aggs)
	} else if len(lastChoiceMessageTools) > 0 {
		toolCalls = lastChoiceMessageTools
	}
	reasoning = reasoningBuf.String()
	if reasoning == "" {
		reasoning = lastChoiceReasoning
		// 兼容实现：reasoning 只在最后一帧 message 里下发（无 delta），一次性补发。
		if onReasoning != nil && reasoning != "" {
			_ = onReasoning(reasoning)
		}
	}
	return contentBuf.String(), reasoning, toolCalls, finishReason, usage, nil
}

type streamChunk struct {
	Usage   *StreamUsage `json:"usage"`
	Choices []struct {
		Delta        streamDelta `json:"delta"`
		FinishReason *string     `json:"finish_reason"`
		Message      *struct {
			ToolCalls        []ToolCall `json:"tool_calls"`
			Content          string     `json:"content"`
			ReasoningContent string     `json:"reasoning_content"`
		} `json:"message"`
	} `json:"choices"`
}

type streamDelta struct {
	Role             string           `json:"role"`
	Content          string           `json:"content"`
	ReasoningContent string           `json:"reasoning_content"`
	ToolCalls        []streamToolPart `json:"tool_calls"`
}

type streamToolPart struct {
	Index    int        `json:"index"`
	ID       string     `json:"id"`
	Type     string     `json:"type"`
	Function streamFunc `json:"function"`
}

type streamFunc struct {
	Name      string
	Arguments string
}

// UnmarshalJSON 兼容 arguments 为 string 或 JSON object/array（部分兼容端在 SSE 里发 object）。
func (f *streamFunc) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	f.Name = raw.Name
	if len(raw.Arguments) == 0 || string(raw.Arguments) == "null" {
		f.Arguments = ""
		return nil
	}
	if raw.Arguments[0] == '"' {
		var s string
		if err := json.Unmarshal(raw.Arguments, &s); err != nil {
			return err
		}
		f.Arguments = s
		return nil
	}
	f.Arguments = string(raw.Arguments)
	return nil
}

type streamToolAgg struct {
	Index int
	ID    string
	Type  string
	Name  string
	Args  strings.Builder
}

func mergeToolDelta(aggs map[int]*streamToolAgg, td streamToolPart) {
	a := aggs[td.Index]
	if a == nil {
		a = &streamToolAgg{Index: td.Index}
		aggs[td.Index] = a
	}
	if td.ID != "" {
		a.ID = td.ID
	}
	if td.Type != "" {
		a.Type = td.Type
	}
	if td.Function.Name != "" {
		a.Name = td.Function.Name
	}
	a.Args.WriteString(td.Function.Arguments)
}

func finalizeStreamToolCalls(aggs map[int]*streamToolAgg) []ToolCall {
	keys := make([]int, 0, len(aggs))
	for k := range aggs {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	out := make([]ToolCall, 0, len(keys))
	for _, k := range keys {
		a := aggs[k]
		t := a.Type
		if t == "" {
			t = "function"
		}
		out = append(out, ToolCall{
			ID:   a.ID,
			Type: t,
			Function: ToolCallFunction{
				Name:      a.Name,
				Arguments: a.Args.String(),
			},
		})
	}
	return NormalizeToolCalls(out)
}

// streamRoundFlags 可选流式轮次行为（worker 等）。
type streamRoundFlags struct {
	noBrainInject   bool
	brainProfile    brain.PromptProfile
	disableThinking bool
	logKind         string
	subagentID      string
	sessionID       string
	logOutputCwd    string
}

// ChatStreamRound 单次流式 chat/completions 请求（主 chat：注入 brain）。
func (c *Client) ChatStreamRound(ctx context.Context, messages []Message, tools []Tool, toolChoice string, maxTokens int, temperature float64, onDelta func(string) error) (assistant string, reasoning string, toolCalls []ToolCall, finishReason string, usage StreamUsage, err error) {
	return c.chatStreamRound(ctx, messages, tools, toolChoice, maxTokens, temperature, streamRoundFlags{}, onDelta, nil)
}

// ChatStreamRoundFor 与 ChatStreamRound 相同，但显式指定 brain 注入档位与 LLM 日志产出区
// （多 cata 并行时避免依赖全局 SetPromptProfile/OutputCwd）。
func (c *Client) ChatStreamRoundFor(ctx context.Context, messages []Message, tools []Tool, toolChoice string, maxTokens int, temperature float64, profile brain.PromptProfile, logOutputCwd string, onDelta func(string) error, onReasoning func(string) error) (assistant string, reasoning string, toolCalls []ToolCall, finishReason string, usage StreamUsage, err error) {
	return c.chatStreamRound(ctx, messages, tools, toolChoice, maxTokens, temperature, streamRoundFlags{
		brainProfile: profile,
		logOutputCwd: logOutputCwd,
	}, onDelta, onReasoning)
}

// ChatWorkerStreamRound 子 Agent 流式轮次：minimal 脑子注入、低温度、禁用 thinking。
func (c *Client) ChatWorkerStreamRound(ctx context.Context, messages []Message, tools []Tool, maxTokens int, meta WorkerRoundMeta, onDelta func(string) error) (assistant string, reasoning string, toolCalls []ToolCall, finishReason string, usage StreamUsage, err error) {
	logCwd := ""
	if cc := brain.ChatContextFrom(ctx); cc != nil {
		logCwd = cc.OutputCwd
	}
	return c.chatStreamRound(ctx, messages, tools, "auto", maxTokens, 0.2, streamRoundFlags{
		brainProfile:    brain.PromptProfileMinimal,
		disableThinking: true,
		logKind:         "worker_round",
		subagentID:      meta.SubagentID,
		sessionID:       meta.SessionID,
		logOutputCwd:    logCwd,
	}, onDelta, nil)
}

func (c *Client) chatStreamRound(ctx context.Context, messages []Message, tools []Tool, toolChoice string, maxTokens int, temperature float64, flags streamRoundFlags, onDelta func(string) error, onReasoning func(string) error) (assistant string, reasoning string, toolCalls []ToolCall, finishReason string, usage StreamUsage, err error) {
	if maxTokens <= 0 {
		maxTokens = c.maxTokens
	}
	if temperature <= 0 {
		temperature = 0.7
	}
	req := ChatRequest{
		Model:           c.model,
		Messages:        SanitizeMessagesToolCalls(messages),
		MaxTokens:       maxTokens,
		Temperature:     temperature,
		NoBrainInject:   flags.noBrainInject,
		BrainProfile:    flags.brainProfile,
		DisableThinking: flags.disableThinking,
		LogKind:         flags.logKind,
		SubagentID:      flags.subagentID,
		SessionID:       flags.sessionID,
		LogOutputCwd:    flags.logOutputCwd,
	}
	hc := c.streamHTTPClient
	if hc == nil {
		hc = c.httpClient
	}

	candidates := c.apiURLCandidates()
	if len(candidates) == 0 {
		return "", "", nil, "", usage, fmt.Errorf("API URL is empty")
	}

	var resp *http.Response
	for i, u := range candidates {
		c.apiURL = u
		httpReq, err := c.buildHTTPChatRequest(ctx, req, tools, toolChoice, true)
		if err != nil {
			return "", "", nil, "", usage, err
		}
		resp, err = hc.Do(httpReq)
		if err != nil {
			return "", "", nil, "", usage, fmt.Errorf("stream request: %w", err)
		}
		if resp.StatusCode == http.StatusOK {
			c.commitAPIURL(u)
			break
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		msg := string(body)
		if len(msg) > 800 {
			msg = msg[:800] + "..."
		}
		if shouldTryAlternateAPIURL(resp.StatusCode, msg) && i+1 < len(candidates) {
			log.Printf("LLM: stream endpoint miss (%d) at %s; trying alternate URL", resp.StatusCode, u)
			continue
		}
		return "", "", nil, "", usage, fmt.Errorf("stream API status %d (url=%s): %s", resp.StatusCode, c.apiURL, msg)
	}
	if resp == nil {
		return "", "", nil, "", usage, fmt.Errorf("stream request: no response")
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") && !strings.Contains(ct, "application/x-ndjson") {
		body, _ := io.ReadAll(resp.Body)
		// 部分代理仍返回 SSE 正文但 Content-Type 标成 application/json。
		if looksLikeSSEChatBody(body) {
			assistant, reasoning, toolCalls, finishReason, usage, err = pickStreamReader(c.apiFormat, c.apiURL, "text/event-stream", bytes.NewReader(body), onDelta, onReasoning)
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return "", "", nil, "", usage, ctxErr
				}
				log.Printf("LLM: mislabeled SSE parse failed (Content-Type=%s): %v; falling back to non-stream", ct, err)
				return c.nonStreamFallbackRound(ctx, messages, tools, toolChoice, maxTokens, temperature, flags, onDelta, onReasoning, usage,
					fmt.Sprintf("mislabeled SSE (Content-Type=%s)", ct))
			}
		} else {
			var perr error
			assistant, toolCalls, perr = c.adapter.ParseResponse(body)
			if perr != nil {
				log.Printf("LLM: stream got non-SSE JSON parse error (Content-Type=%s): %v; falling back to non-stream; body=%s",
					ct, perr, truncateForErr(body, 200))
				return c.nonStreamFallbackRound(ctx, messages, tools, toolChoice, maxTokens, temperature, flags, onDelta, onReasoning, usage,
					fmt.Sprintf("non-SSE Content-Type=%s", ct))
			}
			finishReason = "stop"
			if rawReasoning := extractReasoningFromChatJSON(body); rawReasoning != "" {
				reasoning = rawReasoning
				if onReasoning != nil {
					_ = onReasoning(rawReasoning)
				}
			}
		}
		if emptyLLMResult(assistant, reasoning, toolCalls) {
			log.Printf("LLM: stream endpoint returned empty non-SSE body (Content-Type=%s); falling back to non-stream; body=%s",
				ct, truncateForErr(body, 200))
			return c.nonStreamFallbackRound(ctx, messages, tools, toolChoice, maxTokens, temperature, flags, onDelta, onReasoning, usage,
				"empty non-SSE body")
		}
		sendAssistantDelta(onDelta, onReasoning, assistant, reasoning)
		c.appendLLMLog(req, tools, toolChoice, assistantText(assistant, reasoning), toolCalls, body)
		return assistantText(assistant, reasoning), reasoning, toolCalls, finishReason, usage, nil
	}

	assistant, reasoning, toolCalls, finishReason, usage, err = pickStreamReader(c.apiFormat, c.apiURL, ct, resp.Body, onDelta, onReasoning)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", "", nil, "", usage, ctxErr
		}
		log.Printf("LLM: SSE read failed: %v; falling back to non-stream", err)
		return c.nonStreamFallbackRound(ctx, messages, tools, toolChoice, maxTokens, temperature, flags, onDelta, onReasoning, usage, "SSE read error")
	}

	// 正文里嵌入的 [tool_call …] / <tool_call> 也可补救（部分端 finish_reason=tool_calls 但 delta 为空）
	if len(toolCalls) == 0 && len(tools) > 0 {
		if embedded, stripped := ParseEmbeddedToolCalls(assistant); len(embedded) > 0 {
			toolCalls = embedded
			assistant = stripped
			if finishReason == "" {
				finishReason = "tool_calls"
			}
		}
	}

	// 若干 OpenAI 兼容端在 SSE 下 finish_reason=tool_calls 但 delta 未携带可合并的 tool_calls；
	// 再发一次非流式请求拿到完整 tool_calls，才能进入服务端多轮工具循环。
	if (strings.EqualFold(finishReason, "tool_calls") || strings.EqualFold(finishReason, "tool_use")) && len(toolCalls) == 0 && len(tools) > 0 {
		log.Printf("LLM: stream finish_reason=tool_calls but 0 parsed tool_calls; retrying non-stream once")
		return c.nonStreamFallbackRound(ctx, messages, tools, toolChoice, maxTokens, temperature, flags, onDelta, onReasoning, usage, "empty tool_calls")
	}

	if emptyLLMResult(assistant, reasoning, toolCalls) {
		log.Printf("LLM: SSE finished with empty content/tools; falling back to non-stream (slow proxy / thinking models)")
		return c.nonStreamFallbackRound(ctx, messages, tools, toolChoice, maxTokens, temperature, flags, onDelta, onReasoning, usage, "empty SSE")
	}

	out := assistantText(assistant, reasoning)
	c.appendLLMLog(req, tools, toolChoice, out, toolCalls, nil)
	return out, reasoning, toolCalls, finishReason, usage, nil
}

func emptyLLMResult(content, reasoning string, toolCalls []ToolCall) bool {
	return strings.TrimSpace(content) == "" && strings.TrimSpace(reasoning) == "" && len(toolCalls) == 0
}

func extractReasoningFromChatJSON(body []byte) string {
	var wrap struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(body, &wrap) != nil || len(wrap.Choices) == 0 {
		return ""
	}
	return strings.TrimSpace(wrap.Choices[0].Message.ReasoningContent)
}

// sendAssistantDelta 将最终正文交给 onDelta。
// 若 reasoning 已通过 onReasoning 单独下发（--show-thinking），正文只发真实 content，
// 避免同一段推理在 TUI 思考块与主正文重复出现；未开 onReasoning 时保持原 assistantText 回退。
func sendAssistantDelta(onDelta, onReasoning func(string) error, assistant, reasoning string) {
	if onDelta == nil {
		return
	}
	if onReasoning != nil && strings.TrimSpace(reasoning) != "" {
		if text := strings.TrimSpace(assistant); text != "" {
			_ = onDelta(text)
		}
		return
	}
	if text := assistantText(assistant, reasoning); text != "" {
		_ = onDelta(text)
	}
}

// nonStreamFallbackRound 流式不可用/空响应时改走非流式（慢推理代理常见：假 SSE / 早回空 JSON）。
func (c *Client) nonStreamFallbackRound(ctx context.Context, messages []Message, tools []Tool, toolChoice string, maxTokens int, temperature float64, flags streamRoundFlags, onDelta func(string) error, onReasoning func(string) error, usage StreamUsage, why string) (assistant string, reasoning string, toolCalls []ToolCall, finishReason string, outUsage StreamUsage, err error) {
	// 用户已取消（Ctrl+C）：直接返回 ctx 错误，不再发起非流式重试。
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", "", nil, "", usage, ctxErr
	}
	outUsage = usage
	log.Printf("LLM: non-stream fallback (%s)", why)
	nreq := ChatRequest{
		Model:           c.model,
		Messages:        messages,
		MaxTokens:       maxTokens,
		Temperature:     temperature,
		NoBrainInject:   flags.noBrainInject,
		BrainProfile:    flags.brainProfile,
		DisableThinking: flags.disableThinking,
		LogKind:         flags.logKind,
		SubagentID:      flags.subagentID,
		SessionID:       flags.sessionID,
		LogOutputCwd:    flags.logOutputCwd,
	}
	cr, tc, err := c.chatWithContext(ctx, nreq, tools, toolChoice, true)
	if err != nil {
		return "", "", nil, "", outUsage, fmt.Errorf("stream unusable (%s); non-stream fallback failed: %w", why, err)
	}
	toolCalls = NormalizeToolCalls(tc)
	finishReason = "stop"
	if cr != nil && len(cr.Choices) > 0 {
		assistant = strings.TrimSpace(cr.Choices[0].Message.Content)
		reasoning = strings.TrimSpace(cr.Choices[0].Message.ReasoningContent)
		if fr := strings.TrimSpace(cr.Choices[0].FinishReason); fr != "" {
			finishReason = fr
		}
	}
	if emptyLLMResult(assistant, reasoning, toolCalls) {
		return "", "", nil, "", outUsage, fmt.Errorf("empty LLM response after non-stream fallback (%s); proxy may have timed out upstream or dropped the completion", why)
	}
	out := assistantText(assistant, reasoning)
	if onReasoning != nil && reasoning != "" {
		_ = onReasoning(reasoning)
	}
	sendAssistantDelta(onDelta, onReasoning, assistant, reasoning)
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}
	return out, reasoning, toolCalls, finishReason, outUsage, nil
}

// ModelName 当前客户端使用的模型名。
func (c *Client) ModelName() string { return c.model }
