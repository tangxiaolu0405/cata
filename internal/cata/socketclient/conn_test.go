package socketclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
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

// TestBackToBackChatsOnSameConn 回归：同一连接连续两轮 chat，第二轮必须拿到自己的**全部**事件。
//
// 旧实现里每轮 chat 起一个读 goroutine，函数返回时只 close(readStopped) 就返回——
// 那个 goroutine 可能仍阻塞在 ReadBytes 上，把**下一轮的行**读走并丢弃
// （表现：第二轮卡住，或丢前几个事件）。现在收尾会等它真正退出。
func TestBackToBackChatsOnSameConn(t *testing.T) {
	serverEnd, clientEnd := net.Pipe()
	defer serverEnd.Close()

	c := NewConnWithDialer("", "/tmp", func() (net.Conn, error) { return clientEnd, nil })

	const round2Tokens = 30
	go func() {
		br := bufio.NewReader(serverEnd)
		for i := 0; i < 2; i++ {
			line, err := br.ReadBytes('\n')
			if err != nil {
				return
			}
			var req map[string]any
			if json.Unmarshal(bytes.TrimSpace(line), &req) != nil || req["command"] != "chat" {
				return
			}
			if i == 0 {
				// 第一轮：正常收尾。
				for _, ev := range []string{
					`{"type":"progress","message":"warmup"}`,
					`{"type":"token","content":"first"}`,
					`{"type":"done","success":true}`,
				} {
					if _, err := serverEnd.Write([]byte(ev + "\n")); err != nil {
						return
					}
				}
				continue
			}
			// 第二轮：立刻连发多行。上一轮残留的读 goroutine 若还活着，会在这里偷行。
			if _, err := serverEnd.Write([]byte(`{"type":"progress","message":"second"}` + "\n")); err != nil {
				return
			}
			for j := 0; j < round2Tokens; j++ {
				ev := `{"type":"token","content":"t` + strconv.Itoa(j) + `,"}` + "\n"
				if _, err := serverEnd.Write([]byte(ev)); err != nil {
					return
				}
			}
			if _, err := serverEnd.Write([]byte(`{"type":"done","success":true}` + "\n")); err != nil {
				return
			}
		}
	}()

	// 第一轮。
	ctx1, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
	if _, err := c.Chat(ctx1, "hi", &collectHandler{}); err != nil {
		cancel1()
		t.Fatalf("round 1: %v", err)
	}
	cancel1()

	// 第二轮：必须收齐全部 token（丢任何一行都说明被上一轮的读 goroutine 吞了）。
	h := &collectHandler{}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	res, err := c.Chat(ctx2, "hi", h)
	if err != nil {
		t.Fatalf("round 2: %v", err)
	}
	if !res.Success {
		t.Fatalf("round 2 not successful: %+v", res)
	}
	want := ""
	for j := 0; j < round2Tokens; j++ {
		want += "t" + strconv.Itoa(j) + ","
	}
	if got := h.tokens.String(); got != want {
		t.Fatalf("round 2 tokens=\n%q\nwant\n%q（有行被上一轮的读 goroutine 吞掉）", got, want)
	}
	if n := h.progress.Load(); n != 1 {
		t.Fatalf("round 2 progress count=%d want 1（首行丢失）", n)
	}
}

// collectHandler 记录 token 与 progress 计数的最小 StreamHandler。
type collectHandler struct {
	tokens   strings.Builder
	progress atomic.Int32
}

func (h *collectHandler) OnToken(s string)   { h.tokens.WriteString(s) }
func (h *collectHandler) OnProgress(string)  { h.progress.Add(1) }
func (h *collectHandler) OnToolStart(string) {}
func (h *collectHandler) ConfirmExec(context.Context, ExecConfirmPrompt) (bool, error) {
	return false, nil
}
func (h *collectHandler) Choose(context.Context, UserChoicePrompt) ([]string, error) {
	return nil, nil
}

// TestChatRetriesOnDeadConn 回归 QQ 渠道故障：缓存连接在对端（agent）空闲退出后仍被复用，
// 首次读写立刻报 "io: read/write on closed pipe"。修复后应自动失效 + 重拨一次并重发，
// 用户消息不丢失（remote 模式 dialFunc 会经隧道重新 EnsureAgent 拉起 agent）。
func TestChatRetriesOnDeadConn(t *testing.T) {
	var dials atomic.Int32

	// 第一次拨号返回「对端已关」的连接（写即 EPIPE）；第二次返回可用的 pipe。
	serverEnd, clientEnd := net.Pipe()
	defer serverEnd.Close()

	c := NewConnWithDialer("", "/tmp", func() (net.Conn, error) {
		if dials.Add(1) == 1 {
			return deadPipeConn{}, nil
		}
		return clientEnd, nil
	})

	// 扮演 server：只服务第二次拨号（第一次是死连接，写不进去）。
	gotText := make(chan string, 1)
	go func() {
		br := bufio.NewReader(serverEnd)
		line, err := br.ReadBytes('\n')
		if err != nil {
			gotText <- ""
			return
		}
		var req map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(line), &req); err != nil {
			gotText <- ""
			return
		}
		text, _ := req["text"].(string)
		gotText <- text
		_, _ = serverEnd.Write([]byte(`{"type":"token","content":"pong"}` + "\n"))
		_, _ = serverEnd.Write([]byte(`{"type":"done","success":true}` + "\n"))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := c.Chat(ctx, "hello-after-idle", nil)
	if err != nil {
		t.Fatalf("chat should self-heal on dead conn, got err: %v", err)
	}
	if !res.Success || res.Text != "pong" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if n := dials.Load(); n != 2 {
		t.Fatalf("dial count=%d want 2（应重拨一次）", n)
	}
	select {
	case text := <-gotText:
		if text != "hello-after-idle" {
			t.Fatalf("server got text %q want %q（消息必须重发，不能丢）", text, "hello-after-idle")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server never received the retried chat")
	}
}

// deadPipeConn 模拟「对端已关闭」的连接：Write 返回 EPIPE，Read 返回 EOF。
type deadPipeConn struct{}

func (deadPipeConn) Read([]byte) (int, error)    { return 0, io.ErrClosedPipe }
func (deadPipeConn) Write([]byte) (int, error)   { return 0, io.ErrClosedPipe }
func (deadPipeConn) Close() error                { return nil }
func (deadPipeConn) LocalAddr() net.Addr         { return dummyAddr{} }
func (deadPipeConn) RemoteAddr() net.Addr        { return dummyAddr{} }
func (deadPipeConn) SetDeadline(time.Time) error { return nil }
func (deadPipeConn) SetReadDeadline(time.Time) error {
	return nil
}
func (deadPipeConn) SetWriteDeadline(time.Time) error { return nil }
