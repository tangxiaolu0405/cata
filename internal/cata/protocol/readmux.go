package protocol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

type chatConnKey struct{}

// WithChatConnReader 将 connLineReader 注入 ctx（流式 chat 轮次内消费客户端行）。
func WithChatConnReader(ctx context.Context, lr *ConnLineReader) context.Context {
	return context.WithValue(ctx, chatConnKey{}, lr)
}

// ConnLineReaderFrom 返回 ctx 中的 ConnLineReader；未注入时返回 nil。
func ConnLineReaderFrom(ctx context.Context) *ConnLineReader {
	lr, _ := ctx.Value(chatConnKey{}).(*ConnLineReader)
	return lr
}

// ConnLineReader multiplexes client→server lines during a streaming chat round
// (chat_cancel, exec_confirm, user_choice). It must share br with the connection
// read loop — a second bufio.Reader on the same net.Conn steals the next chat line.
type ConnLineReader struct {
	conn     net.Conn
	br       *bufio.Reader
	inbox    chan json.RawMessage
	onCancel func()
	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewConnLineReader 创建并启动行复用器。
func NewConnLineReader(br *bufio.Reader, conn net.Conn, onCancel func()) *ConnLineReader {
	r := &ConnLineReader{
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

// Stop 停止行复用器，等 pump 退出后归还 br 给主连接循环。
func (r *ConnLineReader) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
		// Unblock pump if it is waiting on ReadBytes; then wait until it exits
		// so the main connection loop can read the next chat command from br.
		_ = r.conn.SetReadDeadline(time.Now())
		r.wg.Wait()
		clearConnReadDeadline(r.conn)
	})
}

func (r *ConnLineReader) pump() {
	defer r.wg.Done()
	defer close(r.inbox)
	for {
		if r.stopped() {
			return
		}
		raw, err := r.readLine()
		if err != nil {
			// 连接断开/异常时也立即取消当前流（onCancel=ctx cancel），
			// 避免后台 LLM 一直跑到结束才 cancel。
			if r.onCancel != nil && !r.stopped() {
				r.onCancel()
			}
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
		// 非 cancel/确认命令（chat_reset/ping/未知）在流式 chat 期间不进入 inbox：
		// 直接回写 busy 响应，避免堆积在 inbox 卡住 pump（进而卡住后续 chat_cancel），
		// 也让客户端明确知道命令被拒而非静默吞掉。
		if hdr.Command != "exec_confirm" && hdr.Command != "user_choice" {
			_ = r.respondBusy(hdr.Command)
			continue
		}
		select {
		case r.inbox <- raw:
		case <-r.stopCh:
			// 竞态窗口：chat 结束瞬间客户端已发送的确认/下一行可能正被 pump 读到。
			// stopCh 已关闭时尝试非阻塞放入 inbox，让主循环在 chat 返回后 drain，
			// 避免该行随 pump 一起被丢弃。
			select {
			case r.inbox <- raw:
			default:
			}
			return
		}
	}
}

func (r *ConnLineReader) stopped() bool {
	select {
	case <-r.stopCh:
		return true
	default:
		return false
	}
}

// readLine blocks until a non-empty client line, stop, or connection error.
// Socket read timeouts are retried so long LLM/tool rounds do not kill the pump.
func (r *ConnLineReader) readLine() (json.RawMessage, error) {
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

// WaitLine 等待客户端确认/选择响应，直到 deadline / ctx 取消 / 停止。
func (r *ConnLineReader) WaitLine(ctx context.Context, deadline time.Time) (json.RawMessage, error) {
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

// DrainPending 非阻塞取出 inbox 中残留的行（chat 结束后主循环调用，
// 处理 pump 在停止竞态窗口放入的确认/命令行）。
func (r *ConnLineReader) DrainPending() (json.RawMessage, bool) {
	select {
	case raw, ok := <-r.inbox:
		return raw, ok
	default:
		return nil, false
	}
}

// respondBusy 向客户端回写一条明确的 busy 响应（流式 chat 期间收到非流命令）。
func (r *ConnLineReader) respondBusy(command string) error {
	resp, err := json.Marshal(map[string]interface{}{
		"success": false,
		"message": "busy: chat stream in progress; commands other than chat_cancel/exec_confirm/user_choice are not served until the round ends",
	})
	if err != nil {
		return err
	}
	_, err = r.conn.Write(append(resp, '\n'))
	if err == nil {
		log.Printf("ConnLineReader: rejected %q during chat stream", command)
	}
	return err
}
