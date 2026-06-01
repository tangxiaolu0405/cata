package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"crypto/rand"
	"time"
)

const confirmTimeout = 10 * time.Minute

func newConfirmID() string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// waitExecClientConfirm blocks until the client sends exec_confirm with the matching confirmID.
func (ss *SocketServer) waitExecClientConfirm(ctx context.Context, confirmID string) (bool, error) {
	lr := connLineReaderFrom(ctx)
	if lr == nil {
		return false, fmt.Errorf("no chat connection reader")
	}
	deadline := time.Now().Add(confirmTimeout)
	for {
		raw, err := lr.waitLine(ctx, deadline)
		if err != nil {
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

// waitUserChoice blocks until the client sends user_choice with the matching choiceID.
func (ss *SocketServer) waitUserChoice(ctx context.Context, choiceID string) ([]string, error) {
	lr := connLineReaderFrom(ctx)
	if lr == nil {
		return nil, fmt.Errorf("no chat connection reader")
	}
	deadline := time.Now().Add(confirmTimeout)
	for {
		raw, err := lr.waitLine(ctx, deadline)
		if err != nil {
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
