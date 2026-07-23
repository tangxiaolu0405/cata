package llm

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ReadOpenAIResponsesStream 读取 OpenAI/xAI Responses API 的 SSE。
// 同时容忍个别网关仍下发 chat.completions 风格 choices[].delta。
func ReadOpenAIResponsesStream(r io.Reader, onDelta func(string) error) (content string, reasoning string, toolCalls []ToolCall, finishReason string, usage StreamUsage, err error) {
	br := bufio.NewReader(r)
	var contentBuf strings.Builder
	aggs := make(map[int]*streamToolAgg)

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
		if strings.HasPrefix(line, "event:") {
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

		// Chat Completions 风格（兼容回退）
		var chunk streamChunk
		if e := json.Unmarshal([]byte(payload), &chunk); e == nil && len(chunk.Choices) > 0 {
			ch := chunk.Choices[0]
			if ch.FinishReason != nil && *ch.FinishReason != "" {
				finishReason = *ch.FinishReason
			}
			if chunk.Usage != nil {
				usage = *chunk.Usage
			}
			if d := ch.Delta; d.Content != "" {
				contentBuf.WriteString(d.Content)
				if onDelta != nil {
					if e := onDelta(d.Content); e != nil {
						return "", "", nil, "", usage, e
					}
				}
			}
			for _, td := range ch.Delta.ToolCalls {
				mergeToolDelta(aggs, td)
			}
			if readErr == io.EOF {
				break
			}
			continue
		}

		var evt struct {
			Type         string          `json:"type"`
			Delta        json.RawMessage `json:"delta"`
			Text         string          `json:"text"`
			Status       string          `json:"status"`
			FinishReason string          `json:"finish_reason"`
		}
		if e := json.Unmarshal([]byte(payload), &evt); e != nil {
			if readErr == io.EOF {
				break
			}
			continue
		}

		switch {
		case strings.Contains(evt.Type, "output_text.delta"), evt.Type == "response.output_text.delta":
			piece := responsesDeltaText(evt.Delta)
			if piece == "" {
				piece = evt.Text
			}
			if piece != "" {
				contentBuf.WriteString(piece)
				if onDelta != nil {
					if e := onDelta(piece); e != nil {
						return "", "", nil, "", usage, e
					}
				}
			}
		case strings.Contains(evt.Type, "completed"), evt.Type == "response.completed":
			if finishReason == "" {
				finishReason = "stop"
			}
		case evt.FinishReason != "":
			finishReason = evt.FinishReason
		case evt.Status == "completed":
			if finishReason == "" {
				finishReason = "stop"
			}
		}

		if readErr == io.EOF {
			break
		}
	}

	toolCalls = finalizeStreamToolCalls(aggs)
	if finishReason == "" && contentBuf.Len() > 0 {
		finishReason = "stop"
	}
	return contentBuf.String(), reasoning, toolCalls, finishReason, usage, nil
}

func responsesDeltaText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj struct {
		Text    string `json:"text"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		if obj.Text != "" {
			return obj.Text
		}
		return obj.Content
	}
	return ""
}
