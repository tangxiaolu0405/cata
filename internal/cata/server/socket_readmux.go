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
// (chat_cancel, exec_confirm, user_choice). It must share br with the connection
// read loop — a second bufio.Reader on the same net.Conn steals the next chat line.
type connLineReader struct {
	conn     net.Conn
	br       *bufio.Reader
	inbox    chan json.RawMessage
	onCancel func()
	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

func newConnLineReader(br *bufio.Reader, conn net.Conn, onCancel func()) *connLineReader {
	r := &connLineReader{
		conn:     conn,
		br:       br,
		inbox:    make(chan json.RawMessage, 8),
		onCancel: onCancel,
		stopCh:   make(chan struct{}),
	}
	r.wg.Add(1)
	go r.pump()
	return r
}

func clearConnReadDeadline(conn net.Conn) {
	if conn != nil {
		_ = conn.SetReadDeadline(time.Time{})
	}
}

func (r *connLineReader) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
		// Unblock pump if it is waiting on ReadBytes; then wait until it exits
		// so the main connection loop can read the next chat command from br.
		_ = r.conn.SetReadDeadline(time.Now())
		r.wg.Wait()
		clearConnReadDeadline(r.conn)
	})
}

func (r *connLineReader) pump() {
	defer r.wg.Done()
	defer close(r.inbox)
	for {
		if r.stopped() {
			return
		}
		raw, err := r.readLine()
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

func (r *connLineReader) stopped() bool {
	select {
	case <-r.stopCh:
		return true
	default:
		return false
	}
}

// readLine blocks until a non-empty client line, stop, or connection error.
// Socket read timeouts are retried so long LLM/tool rounds do not kill the pump.
func (r *connLineReader) readLine() (json.RawMessage, error) {
	for {
		select {
		case <-r.stopCh:
			return nil, fmt.Errorf("stopped")
		default:
		}
		_ = r.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		line, err := r.br.ReadBytes('\n')
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			select {
			case <-r.stopCh:
				return nil, fmt.Errorf("stopped")
			default:
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
