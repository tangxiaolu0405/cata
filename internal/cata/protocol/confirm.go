package protocol

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ConfirmTimeout 执行确认 / 用户选择的等待上限。
const ConfirmTimeout = 10 * time.Minute

// NewConfirmID 生成一次性确认 ID。
func NewConfirmID() string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// WaitExecClientConfirm 阻塞直到客户端发送 exec_confirm 且 confirmID 匹配。
// lr 来自 ctx（ConnLineReaderFrom），缺失时报错。ctx 取消按未批准处理（返回 false）。
func WaitExecClientConfirm(ctx context.Context, confirmID string) (bool, error) {
	lr := ConnLineReaderFrom(ctx)
	if lr == nil {
		return false, fmt.Errorf("no chat connection reader")
	}
	deadline := time.Now().Add(ConfirmTimeout)
	for {
		raw, err := lr.WaitLine(ctx, deadline)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				return false, nil
			}
			return false, fmt.Errorf("read exec_confirm: %w", err)
		}
		var req struct {
			Command   string `json:"command"`
			ConfirmID string `json:"confirm_id"`
			Approved  bool   `json:"approved"`
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			return false, fmt.Errorf("invalid exec_confirm: %w", err)
		}
		if req.Command != "exec_confirm" {
			continue
		}
		if req.ConfirmID != confirmID {
			return false, fmt.Errorf("confirm_id mismatch")
		}
		return req.Approved, nil
	}
}

// WaitUserChoice 阻塞直到客户端发送 user_choice 且 choiceID 匹配。
// ctx 取消按空选择处理（返回 nil）。
func WaitUserChoice(ctx context.Context, choiceID string) ([]string, error) {
	lr := ConnLineReaderFrom(ctx)
	if lr == nil {
		return nil, fmt.Errorf("no chat connection reader")
	}
	deadline := time.Now().Add(ConfirmTimeout)
	for {
		raw, err := lr.WaitLine(ctx, deadline)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				return nil, nil
			}
			return nil, fmt.Errorf("read user_choice: %w", err)
		}
		var req struct {
			Command  string   `json:"command"`
			ChoiceID string   `json:"choice_id"`
			Selected []string `json:"selected"`
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("invalid user_choice: %w", err)
		}
		if req.Command != "user_choice" {
			continue
		}
		if req.ChoiceID != choiceID {
			return nil, fmt.Errorf("choice_id mismatch")
		}
		return req.Selected, nil
	}
}
