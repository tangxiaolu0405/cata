package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
)

// ReadOpenAIChatStream 读取 OpenAI 兼容的 text/event-stream（data: JSON 行），
// 将 assistant 文本增量交给 onDelta，并返回合并正文、工具调用与 finish_reason。
func ReadOpenAIChatStream(r io.Reader, onDelta func(string) error) (content string, reasoning string, toolCalls []ToolCall, finishReason string, usage StreamUsage, err error) {
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
	Index    int `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
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
	disableThinking bool
	logKind         string
	subagentID      string
	sessionID       string
}

// ChatStreamRound 单次流式 chat/completions 请求（主 chat：注入 brain）。
func (c *Client) ChatStreamRound(ctx context.Context, messages []Message, tools []Tool, toolChoice string, maxTokens int, temperature float64, onDelta func(string) error) (assistant string, reasoning string, toolCalls []ToolCall, finishReason string, usage StreamUsage, err error) {
	return c.chatStreamRound(ctx, messages, tools, toolChoice, maxTokens, temperature, streamRoundFlags{}, onDelta)
}

// ChatWorkerStreamRound 子 Agent 流式轮次：不注入 brain、低温度、禁用 thinking。
func (c *Client) ChatWorkerStreamRound(ctx context.Context, messages []Message, tools []Tool, maxTokens int, meta WorkerRoundMeta, onDelta func(string) error) (assistant string, reasoning string, toolCalls []ToolCall, finishReason string, usage StreamUsage, err error) {
	return c.chatStreamRound(ctx, messages, tools, "auto", maxTokens, 0.2, streamRoundFlags{
		noBrainInject:   true,
		disableThinking: true,
		logKind:         "worker_round",
		subagentID:      meta.SubagentID,
		sessionID:       meta.SessionID,
	}, onDelta)
}

func (c *Client) chatStreamRound(ctx context.Context, messages []Message, tools []Tool, toolChoice string, maxTokens int, temperature float64, flags streamRoundFlags, onDelta func(string) error) (assistant string, reasoning string, toolCalls []ToolCall, finishReason string, usage StreamUsage, err error) {
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
		DisableThinking: flags.disableThinking,
		LogKind:         flags.logKind,
		SubagentID:      flags.subagentID,
		SessionID:       flags.sessionID,
	}
	httpReq, err := c.buildHTTPChatRequest(ctx, req, tools, toolChoice, true)
	if err != nil {
		return "", "", nil, "", usage, err
	}

	hc := c.streamHTTPClient
	if hc == nil {
		hc = c.httpClient
	}
	resp, err := hc.Do(httpReq)
	if err != nil {
		return "", "", nil, "", usage, fmt.Errorf("stream request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		msg := string(body)
		if len(msg) > 800 {
			msg = msg[:800] + "..."
		}
		return "", "", nil, "", usage, fmt.Errorf("stream API status %d (url=%s): %s", resp.StatusCode, c.apiURL, msg)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") && !strings.Contains(ct, "application/x-ndjson") {
		body, _ := io.ReadAll(resp.Body)
		content, toolCalls2, perr := c.adapter.ParseResponse(body)
		if perr != nil {
			return "", "", nil, "", usage, fmt.Errorf("expected SSE stream (Content-Type=%s), got parse error: %v", ct, perr)
		}
		if onDelta != nil && content != "" {
			_ = onDelta(content)
		}
		c.appendLLMLog(req, tools, toolChoice, content, toolCalls2, body)
		return content, "", toolCalls2, "stop", usage, nil
	}

	assistant, reasoning, toolCalls, finishReason, usage, err = pickStreamReader(c.apiFormat, ct, resp.Body, onDelta)
	if err != nil {
		return "", "", nil, "", usage, err
	}

	// 若干 OpenAI 兼容端在 SSE 下 finish_reason=tool_calls 但 delta 未携带可合并的 tool_calls；
	// 再发一次非流式请求拿到完整 tool_calls，才能进入服务端多轮工具循环。
	if (strings.EqualFold(finishReason, "tool_calls") || strings.EqualFold(finishReason, "tool_use")) && len(toolCalls) == 0 && len(tools) > 0 {
		log.Printf("LLM: stream finish_reason=tool_calls but 0 parsed tool_calls; retrying non-stream once")
		nreq := ChatRequest{
			Model:           c.model,
			Messages:        messages,
			MaxTokens:       maxTokens,
			Temperature:     temperature,
			NoBrainInject:   flags.noBrainInject,
			DisableThinking: flags.disableThinking,
			LogKind:         flags.logKind,
			SubagentID:      flags.subagentID,
			SessionID:       flags.sessionID,
		}
		cr, tc2, err2 := c.chat(nreq, tools, toolChoice, true)
		if err2 != nil {
			return assistant, reasoning, toolCalls, finishReason, usage, fmt.Errorf("stream tool_calls empty, non-stream fallback failed: %w", err2)
		}
		if len(tc2) == 0 {
			return assistant, reasoning, toolCalls, finishReason, usage, fmt.Errorf("stream and non-stream both returned no tool_calls while finish_reason implies tools")
		}
		toolCalls = tc2
		if cr != nil && len(cr.Choices) > 0 {
			fb := strings.TrimSpace(cr.Choices[0].Message.Content)
			if fb != "" {
				if strings.TrimSpace(assistant) == "" && onDelta != nil {
					_ = onDelta(fb)
				}
				assistant = fb
			}
			if strings.TrimSpace(cr.Choices[0].Message.ReasoningContent) != "" {
				reasoning = cr.Choices[0].Message.ReasoningContent
			}
		}
		finishReason = "tool_calls"
	}

	c.appendLLMLog(req, tools, toolChoice, assistant, toolCalls, nil)
	return assistant, reasoning, toolCalls, finishReason, usage, nil
}

// ModelName 当前客户端使用的模型名。
func (c *Client) ModelName() string { return c.model }
