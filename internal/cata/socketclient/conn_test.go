package socketclient

import (
	"context"
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
