package socketclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"
)

// TestConnInvalidateMarksUnhealthy 回归：读写错误后 invalidate 使连接失效，
// Healthy() 返回 false，下次 Chat 自动重拨（隧道抖动自愈）。
func TestConnInvalidateMarksUnhealthy(t *testing.T) {
	dialCount := 0
	c := NewConnWithDialer("", "/tmp", func() (net.Conn, error) {
		dialCount++
		// 返回一个立即关闭的连接，模拟读失败触发 invalidate。
		return &closedConn{}, nil
	})

	// 第一次 Chat：写可能失败或读失败，连接应被 invalidate。
	_, _ = c.Chat(context.Background(), "hi", nil)

	if c.Healthy() {
		t.Fatal("conn should be unhealthy after read failure")
	}
}

// closedConn 是一个立即返回 EOF 的 net.Conn 假件。
type closedConn struct{}

func (closedConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (closedConn) Write(p []byte) (int, error)      { return len(p), nil }
func (closedConn) Close() error                     { return nil }
func (closedConn) LocalAddr() net.Addr              { return dummyAddr{} }
func (closedConn) RemoteAddr() net.Addr             { return dummyAddr{} }
func (closedConn) SetDeadline(time.Time) error      { return nil }
func (closedConn) SetReadDeadline(time.Time) error  { return nil }
func (closedConn) SetWriteDeadline(time.Time) error { return nil }

type dummyAddr struct{}

func (dummyAddr) Network() string { return "dummy" }
func (dummyAddr) String() string  { return "dummy" }

// TestChatCancelOnContextCancel 验证「停止生成」核心链路：Chat 的 ctx 取消后，
// Conn 会向 server 发送 chat_cancel（前端 abort → gateway r.Context() 取消 → 此路径）。
func TestChatCancelOnContextCancel(t *testing.T) {
	// net.Pipe：serverEnd 由测试扮演 server，clientEnd 给 Conn 拨号。
	serverEnd, clientEnd := net.Pipe()
	defer serverEnd.Close()

	c := NewConnWithDialer("", "/tmp", func() (net.Conn, error) {
		return clientEnd, nil
	})

	// 后台：扮演 server，读 chat 请求，然后等待 chat_cancel。
	cancelSeen := make(chan bool, 1)
	go func() {
		br := bufio.NewReader(serverEnd)
		line, err := br.ReadBytes('\n')
		if err != nil {
			cancelSeen <- false
			return
		}
		var req map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(line), &req); err != nil {
			cancelSeen <- false
			return
		}
		if req["command"] != "chat" {
			cancelSeen <- false
			return
		}
		// 不发任何事件：等待客户端 ctx 取消后发 chat_cancel。
		_ = serverEnd.SetReadDeadline(time.Now().Add(3 * time.Second))
		line2, err := br.ReadBytes('\n')
		if err != nil {
			cancelSeen <- false
			return
		}
		var cancelReq map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(line2), &cancelReq); err != nil {
			cancelSeen <- false
			return
		}
		ok := cancelReq["command"] == "chat_cancel"
		// server 收到 chat_cancel 后收尾：回 done（客户端等待此事件才返回）。
		if ok {
			done, _ := json.Marshal(map[string]any{"type": "done", "success": false, "cancelled": true})
			_, _ = serverEnd.Write(append(done, '\n'))
		}
		cancelSeen <- ok
	}()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = c.Chat(ctx, "hello", nil)
		close(done)
	}()

	// 等 chat 请求已发出。
	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case ok := <-cancelSeen:
		if !ok {
			t.Fatal("server did not receive chat_cancel")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for chat_cancel")
	}

	// Chat 应返回 cancelled=true，且连接保持（history 保留，下次可复用）。
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Chat did not return after cancel")
	}
	if !c.Healthy() {
		t.Fatal("conn should stay healthy after cancel (history preserved)")
	}
}
