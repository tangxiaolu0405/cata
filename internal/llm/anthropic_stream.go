package llm

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ReadAnthropicStream 读取 Anthropic Messages SSE（event:/data: 行）。
func ReadAnthropicStream(r io.Reader, onDelta func(string) error) (content string, reasoning string, toolCalls []ToolCall, finishReason string, usage StreamUsage, err error) {
	br := bufio.NewReader(r)
	aggs := make(map[int]*streamToolAgg)
	var contentBuf strings.Builder
	var currentEvent string

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
			currentEvent = ""
			continue
		}
		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			if readErr == io.EOF {
				break
			}
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			if readErr == io.EOF {
				break
			}
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))

		switch currentEvent {
		case "message_delta":
			var wrap struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage *StreamUsage `json:"usage"`
			}
			if json.Unmarshal([]byte(payload), &wrap) == nil {
				if wrap.Delta.StopReason != "" {
					finishReason = wrap.Delta.StopReason
				}
				if wrap.Usage != nil {
					usage = *wrap.Usage
				}
			}
		case "content_block_delta":
			var wrap struct {
				Index int `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if json.Unmarshal([]byte(payload), &wrap) != nil {
				break
			}
			switch wrap.Delta.Type {
			case "text_delta":
				if wrap.Delta.Text != "" {
					contentBuf.WriteString(wrap.Delta.Text)
					if onDelta != nil {
						if e := onDelta(wrap.Delta.Text); e != nil {
							return "", "", nil, "", usage, e
						}
					}
				}
			case "input_json_delta":
				a := aggs[wrap.Index]
				if a == nil {
					a = &streamToolAgg{Index: wrap.Index}
					aggs[wrap.Index] = a
				}
				a.Args.WriteString(wrap.Delta.PartialJSON)
			}
		case "content_block_start":
			var wrap struct {
				Index        int `json:"index"`
				ContentBlock struct {
					Type string `json:"type"`
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"content_block"`
			}
			if json.Unmarshal([]byte(payload), &wrap) == nil && wrap.ContentBlock.Type == "tool_use" {
				a := aggs[wrap.Index]
				if a == nil {
					a = &streamToolAgg{Index: wrap.Index}
					aggs[wrap.Index] = a
				}
				a.ID = wrap.ContentBlock.ID
				a.Name = wrap.ContentBlock.Name
				a.Type = "function"
			}
		case "error":
			var wrap struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal([]byte(payload), &wrap) == nil && wrap.Error.Message != "" {
				return "", "", nil, "", usage, fmt.Errorf("stream API error: %s", wrap.Error.Message)
			}
		}

		if readErr == io.EOF {
			break
		}
	}

	if len(aggs) > 0 {
		toolCalls = finalizeStreamToolCalls(aggs)
	}
	if finishReason == "tool_use" {
		finishReason = "tool_calls"
	}
	return contentBuf.String(), reasoning, toolCalls, finishReason, usage, nil
}

// pickStreamReader 按 api_format / URL 选择流式解析器。
func pickStreamReader(apiFormat, apiURL, contentType string, body io.Reader, onDelta func(string) error, onReasoning func(string) error) (content string, reasoning string, toolCalls []ToolCall, finishReason string, usage StreamUsage, err error) {
	if ResolveAPIFormat(apiFormat, "", "") == APIFormatAnthropic && strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		return ReadAnthropicStream(body, onDelta)
	}
	if isResponsesAPIURL(apiURL) {
		return ReadOpenAIResponsesStream(body, onDelta)
	}
	return ReadOpenAIChatStream(body, onDelta, onReasoning)
}
