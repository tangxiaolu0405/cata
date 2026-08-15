package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"cata/internal/cata/brain"
	"cata/internal/cata/clock"
)

// Chat 发送聊天请求
func (c *Client) Chat(ctx context.Context, messages []Message) (string, error) {
	req := ChatRequest{
		Model:       c.model,
		Messages:    messages,
		MaxTokens:   c.maxTokens,
		Temperature: 0.7,
	}

	resp, _, err := c.chatWithContext(ctx, req, nil, "", false)
	if err != nil {
		return "", fmt.Errorf("failed to chat: %w", err)
	}

	if resp.Error != nil {
		return "", fmt.Errorf("API error: %s (type: %s, code: %s)",
			resp.Error.Message, resp.Error.Type, resp.Error.Code)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no response from LLM")
	}

	return resp.Choices[0].Message.Content, nil
}

// ChatEvolution 演进决策：低温度、不写 llm.log（skipAppendLog）。
// maxTokens=0 时不设 API max_tokens（由模型默认输出上限）；>0 则限制输出长度。
// 不注入 boot-leader / brain 节选；禁用 thinking，避免 JSON 落在 reasoning_content。
func (c *Client) ChatEvolution(ctx context.Context, messages []Message, maxTokens int) (string, error) {
	req := ChatRequest{
		Model:           c.model,
		Messages:        messages,
		MaxTokens:       maxTokens,
		Temperature:     0.2,
		NoBrainInject:   true,
		DisableThinking: true,
	}
	resp, _, err := c.chatWithContext(ctx, req, nil, "", true)
	if err != nil {
		return "", fmt.Errorf("evolution chat: %w", err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("API error: %s", resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no response from LLM")
	}
	msg := resp.Choices[0].Message
	return assistantText(msg.Content, msg.ReasoningContent), nil
}

// ChatWithTools 调用带有 tools 列表的对话接口，返回助手回复和工具调用列表。
// maxTokens / temperature 传 0 则回退到客户端默认值。
func (c *Client) ChatWithTools(ctx context.Context, messages []Message, tools []Tool, toolChoice string, maxTokens int, temperature float64) (string, []ToolCall, error) {
	if maxTokens <= 0 {
		maxTokens = c.maxTokens
	}
	if temperature == 0 {
		temperature = 0.7
	}

	req := ChatRequest{
		Model:       c.model,
		Messages:    messages,
		MaxTokens:   maxTokens,
		Temperature: temperature,
	}

	resp, toolCalls, err := c.chatWithContext(ctx, req, tools, toolChoice, false)
	if err != nil {
		return "", nil, fmt.Errorf("failed to chat with tools: %w", err)
	}

	if resp.Error != nil {
		return "", nil, fmt.Errorf("API error: %s (type: %s, code: %s)",
			resp.Error.Message, resp.Error.Type, resp.Error.Code)
	}

	if len(resp.Choices) == 0 {
		return "", nil, fmt.Errorf("no response from LLM")
	}

	return resp.Choices[0].Message.Content, toolCalls, nil
}

// buildHTTPChatRequest 构建 HTTP 请求（stream 为 true 时使用 SSE）。
// injectBrain 由 req.NoBrainInject 控制；演进模块应设 NoBrainInject=true。
func (c *Client) buildHTTPChatRequest(ctx context.Context, req ChatRequest, tools []Tool, toolChoice string, stream bool) (*http.Request, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("API key is empty")
	}
	msgs := req.Messages
	if !req.NoBrainInject {
		profile := req.BrainProfile
		if profile == "" {
			if cc := brain.ChatContextFrom(ctx); cc != nil && cc.Profile != "" {
				profile = cc.Profile
			}
		}
		if profile == "" {
			profile = brain.ActivePromptProfile()
		}
		msgs = SanitizeMessagesToolCalls(compactMessageContentForAPI(withBootLeaderSystemMessageForCtx(ctx, req.Messages, profile)))
	} else {
		msgs = SanitizeMessagesToolCalls(compactMessageContentForAPI(req.Messages))
	}
	// vLLM 等严格端点：system 只能在最前且通常只要一条（否则 400 System message must be at the beginning）。
	msgs = coalesceSystemMessagesForAPI(msgs)
	req.Messages = msgs
	log.Printf("LLM Request: URL=%s, Model=%s, format=%s label=%s adapter=%T, stream=%v, APIKey present=%v",
		c.apiURL, c.model, c.apiFormat, c.providerLabel, c.adapter, stream, c.apiKey != "")

	httpReq, err := c.adapter.BuildRequest(c.apiURL, c.apiKey, c.model, req.Messages, req.MaxTokens, req.Temperature, tools, toolChoice, stream, req.DisableThinking)
	if err != nil {
		return nil, err
	}
	if ctx != nil {
		httpReq = httpReq.WithContext(ctx)
	}
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	return httpReq, nil
}

// chatWithContext 发送 HTTP 请求到 LLM API（内部统一入口）。ctx 透传给 HTTP 请求；
// skipAppendLog 为 true 时不写 llm.log（由调用方统一写）。
func (c *Client) chatWithContext(ctx context.Context, req ChatRequest, tools []Tool, toolChoice string, skipAppendLog bool) (*ChatResponse, []ToolCall, error) {
	candidates := c.apiURLCandidates()
	if len(candidates) == 0 {
		return nil, nil, fmt.Errorf("API URL is empty")
	}

	var lastStatusErr error
	for i, u := range candidates {
		c.apiURL = u
		httpReq, err := c.buildHTTPChatRequest(ctx, req, tools, toolChoice, false)
		if err != nil {
			return nil, nil, err
		}

		if os.Getenv("DEBUG_LLM") == "true" {
			log.Printf("DEBUG: API URL: %s", c.apiURL)
			log.Printf("DEBUG: Provider adapter: %T format=%s", c.adapter, c.apiFormat)
			log.Printf("DEBUG: API Key length: %d", len(c.apiKey))
			authHeader := httpReq.Header.Get("Authorization")
			if len(authHeader) > 20 {
				log.Printf("DEBUG: Authorization header: %s...", authHeader[:20])
			} else {
				log.Printf("DEBUG: Authorization header: %s", authHeader)
			}
		}

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			log.Printf("HTTP Request failed: URL=%s, Error=%v", c.apiURL, err)
			errStr := err.Error()
			if errStr == "EOF" || strings.Contains(errStr, "EOF") {
				authHeader := httpReq.Header.Get("Authorization")
				authPresent := authHeader != ""
				authPrefix := ""
				if len(authHeader) > 20 {
					authPrefix = authHeader[:20]
				} else {
					authPrefix = authHeader
				}
				helpMsg := ""
				if strings.Contains(c.apiURL, "dashscope") {
					helpMsg = "\nFor Qwen/DashScope EOF errors, try:\n" +
						"1. Verify API key matches your region (China/International/US)\n" +
						"2. Try international endpoint: https://dashscope-intl.aliyuncs.com/compatible-mode/v1/chat/completions\n" +
						"3. Verify API key is valid and has proper permissions\n" +
						"4. Check network connectivity to DashScope servers"
				}
				return nil, nil, fmt.Errorf("connection closed unexpectedly (EOF). URL=%s, AuthHeader present=%v, AuthPrefix=%s.%s\nPossible causes: 1) API key invalid/missing or region mismatch, 2) Network issue, 3) API endpoint incorrect",
					c.apiURL, authPresent, authPrefix, helpMsg)
			}
			return nil, nil, fmt.Errorf("failed to send request: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			log.Printf("Failed to read response body: %v", err)
			return nil, nil, fmt.Errorf("failed to read response: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			c.commitAPIURL(u)
			content, toolCalls, err := c.adapter.ParseResponse(body)
			if err != nil {
				return nil, nil, err
			}
			if !skipAppendLog {
				c.appendLLMLog(req, tools, toolChoice, content, toolCalls, body)
			}
			chatResp := &ChatResponse{
				Choices: []struct {
					Index        int     `json:"index"`
					Message      Message `json:"message"`
					FinishReason string  `json:"finish_reason"`
				}{
					{
						Index: 0,
						Message: Message{
							Role:    "assistant",
							Content: content,
						},
						FinishReason: "stop",
					},
				},
			}
			return chatResp, toolCalls, nil
		}

		errorMsg := string(body)
		if len(errorMsg) > 500 {
			errorMsg = errorMsg[:500] + "..."
		}
		log.Printf("API returned non-200 status: %d, URL=%s, Body: %s", resp.StatusCode, c.apiURL, errorMsg)
		if shouldTryAlternateAPIURL(resp.StatusCode, errorMsg) && i+1 < len(candidates) {
			log.Printf("LLM: endpoint miss (%d) at %s; trying alternate URL", resp.StatusCode, u)
			continue
		}
		if resp.StatusCode == http.StatusNotFound {
			lastStatusErr = fmt.Errorf("API endpoint not found (404). URL=%s. Please check: 1) URL is correct (should end with /chat/completions for OpenAI-compat), 2) Model name is valid, 3) API endpoint matches your region", c.apiURL)
		} else {
			lastStatusErr = fmt.Errorf("API returned status %d: %s", resp.StatusCode, errorMsg)
		}
		return nil, nil, lastStatusErr
	}
	if lastStatusErr != nil {
		return nil, nil, lastStatusErr
	}
	return nil, nil, fmt.Errorf("no API URL candidates")
}

// assistantText 合并 content 与 reasoning_content（DeepSeek thinking 可能只填后者）。
func assistantText(content, reasoning string) string {
	content = strings.TrimSpace(content)
	reasoning = strings.TrimSpace(reasoning)
	if content != "" {
		return content
	}
	return reasoning
}

// inferPromptSources 标注本条请求里各段 system，便于阅读 llm.log。
func inferPromptSources(msgs []Message) []string {
	var out []string
	boot := strings.TrimSpace(loadBootLeaderPrompt())
	i := 0
	for i < len(msgs) && msgs[i].Role == "system" {
		c := strings.TrimSpace(msgs[i].Content)
		switch {
		case boot != "" && c == boot:
			out = append(out, "brain/boot-leader.md")
		case strings.HasPrefix(c, brain.TerminalBundleSystemPrefix):
			out = append(out, "brain/core+workflow+hot (server excerpt)")
		default:
			out = append(out, "system:other")
		}
		i++
	}
	return out
}

// appendLLMLog 将一轮 LLM 对话追加为一行 JSON（JSON Lines）。
// 默认写入 prompt 组件清单（static 仅 chars/preview，conversation 仅末尾几条全文），避免每轮重复刷 boot-leader 全文。
// 设置 LLM_LOG_VERBOSE=1 可恢复完整 messages/tools/raw_body。
// 日志路径由环境变量 LLM_LOG_FILE 控制，默认 llm.log。
func (c *Client) appendLLMLog(req ChatRequest, tools []Tool, toolChoice string, content string, toolCalls []ToolCall, rawBody []byte) {
	logPath := brain.LLMLogPath()
	if out := strings.TrimSpace(req.LogOutputCwd); out != "" {
		logPath = brain.LLMLogPathFor(out)
	}

	msgsCopy := append([]Message(nil), req.Messages...)
	profile := req.BrainProfile
	if profile == "" {
		profile = brain.ActivePromptProfile()
	}
	effectiveMessages := withBootLeaderSystemMessageFor(msgsCopy, profile)

	respLog := map[string]interface{}{
		"content": content,
	}
	if len(toolCalls) > 0 {
		respLog["tool_calls"] = toolCalls
	}

	entry := map[string]interface{}{
		"timestamp":      clock.RFC3339(),
		"kind":           llmLogKind(req),
		"url":            c.apiURL,
		"model":          c.model,
		"prompt_sources": inferPromptSources(effectiveMessages),
		"prompt":         buildPromptManifest(effectiveMessages, tools, toolChoice, req.MaxTokens, req.Temperature),
		"response":       respLog,
	}
	if req.SubagentID != "" {
		entry["subagent_id"] = req.SubagentID
	}
	if req.SessionID != "" {
		entry["session_id"] = req.SessionID
	}
	if llmLogVerbose() {
		entry["request_full"] = map[string]interface{}{
			"messages":    cloneMessagesForLLMLog(effectiveMessages),
			"tools":       tools,
			"tool_choice": toolChoice,
		}
		if len(rawBody) > 0 {
			entry["raw_body"] = string(rawBody)
		}
	}

	data, err := json.Marshal(entry)
	if err != nil {
		log.Printf("Failed to marshal LLM log entry: %v", err)
		return
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to open LLM log file %s: %v", logPath, err)
		return
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		log.Printf("Failed to write LLM log entry to %s: %v", logPath, err)
	}
}

func llmLogKind(req ChatRequest) string {
	if k := strings.TrimSpace(req.LogKind); k != "" {
		return k
	}
	return "chat_round"
}
