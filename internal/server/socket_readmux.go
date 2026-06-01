package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

type chatConnKey struct{}

func withChatConnReader(ctx context.Context, lr *connLineReader) context.Context {
	return context.WithValue(ctx, chatConnKey{}, lr)
}

func connLineReaderFrom(ctx context.Context) *connLineReader {
	lr, _ := ctx.Value(chatConnKey{}).(*connLineReader)
	return lr
}

// connLineReader multiplexes client→server lines during a streaming chat round
// (chat_cancel, exec_confirm, user_choice) on a single bufio.Reader.
type connLineReader struct {
	conn     net.Conn
	br       *bufio.Reader
	inbox    chan json.RawMessage
	onCancel func()
	stopOnce sync.Once
	stopCh   chan struct{}
}

func newConnLineReader(conn net.Conn, onCancel func()) *connLineReader {
	r := &connLineReader{
		conn:     conn,
		br:       bufio.NewReader(conn),
		inbox:    make(chan json.RawMessage, 8),
		onCancel: onCancel,
		stopCh:   make(chan struct{}),
	}
	go r.pump()
	return r
}

func (r *connLineReader) Stop() {
	r.stopOnce.Do(func() { close(r.stopCh) })
}

func (r *connLineReader) pump() {
	defer close(r.inbox)
	for {
		select {
		case <-r.stopCh:
			return
		default:
		}
		raw, err := r.readLine(time.Now().Add(30 * time.Second))
		if err != nil {
			return
		}
		var hdr struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(raw, &hdr); err != nil {
			continue
		}
		if hdr.Command == "chat_cancel" {
			if r.onCancel != nil {
				r.onCancel()
			}
			continue
		}
		select {
		case r.inbox <- raw:
		case <-r.stopCh:
			return
		}
	}
}

func (r *connLineReader) readLine(deadline time.Time) (json.RawMessage, error) {
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("read timed out")
		}
		_ = r.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		line, err := r.br.ReadBytes('\n')
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				select {
				case <-r.stopCh:
					return nil, fmt.Errorf("stopped")
				default:
					continue
				}
			}
			return nil, err
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		_ = r.conn.SetReadDeadline(time.Time{})
		return json.RawMessage(line), nil
	}
}

func (r *connLineReader) waitLine(ctx context.Context, deadline time.Time) (json.RawMessage, error) {
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("confirmation timed out")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-r.stopCh:
			return nil, fmt.Errorf("stopped")
		case raw, ok := <-r.inbox:
			if !ok {
				return nil, fmt.Errorf("connection closed")
			}
			return raw, nil
		case <-time.After(time.Until(deadline)):
		}
	}
}
