package tunnel

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"cata/internal/cata/tunnel"
)

// TestChatCleanupDoesNotHangOnTunnel 端到端回归测试（对应 QQ「消息收到但无回复」根因）：
//
// 复刻 socketclient.chatAsWithAttachmentsOnce 的收尾流程——
//  1. 写入请求行，逐行读到 done
//  2. 收尾：close(readStopped) → SetReadDeadline(now) → 等读 goroutine 退出
//
// 旧实现里 streamConn.SetReadDeadline 是 no-op，第 3 步会永久阻塞（<-readerDone），
// 表现为 gateway 永不打印 `qq: cata chat done`。本测试确保收尾在秒级内完成。
func TestChatCleanupDoesNotHangOnTunnel(t *testing.T) {
	// Unix socket 路径有长度上限（~104 字节），t.TempDir() 在 macOS 上过长，
	// 用短目录 + 短文件名。
	dir, err := os.MkdirTemp("", "ct")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sockPath := dir + "/s"
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// 模拟 cata server：收到一行请求后回两行 NDJSON，再保持连接（不关闭）——
	// 这正是触发死锁的关键：流未关，读 goroutine 会阻塞在 Read 上。
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				for {
					if _, err := br.ReadBytes('\n'); err != nil {
						return
					}
					_, _ = c.Write([]byte(`{"type":"token","content":"你好"}` + "\n"))
					_, _ = c.Write([]byte(`{"type":"done"}` + "\n"))
					// 故意不关连接：模拟 server 保持连接等待下一轮（history per-连接）。
				}
			}(c)
		}
	}()

	reg := NewRegistry()
	ts := httptest.NewServer(Handler(reg, HandlerOptions{Token: "tok"}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/cata/v1/tunnel?agent=ws-1"
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer tok")
	ws, resp, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		t.Fatalf("dial: %v (resp=%v)", err, resp)
	}
	defer ws.Close()

	if err := ws.WriteJSON(tunnel.Frame{
		Type: tunnel.FrameHello, AgentID: "ws-1", Name: "proj", RootPath: "/p",
		Protocol: tunnel.ProtocolName, Version: tunnel.Version,
	}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 5*time.Second, func() bool { return reg.AgentAlive("ws-1") })

	// worker 侧帧循环（简化版：open 拨 socket，双向透传）。
	streams := map[uint64]net.Conn{}
	go func() {
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				return
			}
			var f tunnel.Frame
			if err := json.Unmarshal(msg, &f); err != nil {
				continue
			}
			switch f.Type {
			case tunnel.FrameOpen:
				c, err := net.Dial("unix", sockPath)
				if err != nil {
					_ = ws.WriteJSON(tunnel.Frame{Type: tunnel.FrameError, Stream: f.Stream, Message: err.Error()})
					continue
				}
				streams[f.Stream] = c
				_ = ws.WriteJSON(tunnel.Frame{Type: tunnel.FrameOpened, Stream: f.Stream})
				go func(id uint64, c net.Conn) {
					br := bufio.NewReader(c)
					for {
						line, err := br.ReadBytes('\n')
						if err != nil {
							_ = ws.WriteJSON(tunnel.Frame{Type: tunnel.FrameClose, Stream: id})
							_ = c.Close()
							return
						}
						if err := ws.WriteJSON(tunnel.Frame{Type: tunnel.FrameLine, Stream: id, Data: tunnel.EncodeData(line)}); err != nil {
							_ = c.Close()
							return
						}
					}
				}(f.Stream, c)
			case tunnel.FrameLine:
				data, err := tunnel.DecodeData(f.Data)
				if err != nil {
					continue
				}
				if c, ok := streams[f.Stream]; ok {
					_, _ = c.Write(data)
				}
			case tunnel.FrameClose:
				if c, ok := streams[f.Stream]; ok {
					_ = c.Close()
					delete(streams, f.Stream)
				}
			}
		}
	}()

	conn, err := reg.DialAgent("ws-1")
	if err != nil {
		t.Fatalf("dial agent: %v", err)
	}
	defer conn.Close()

	// 1) 写请求
	if _, err := conn.Write([]byte(`{"command":"chat","text":"你好"}` + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	// 2) 读到 done（复刻 socketclient 的读循环）
	br := bufio.NewReaderSize(conn, 64*1024)
	readStopped := make(chan struct{})
	readerDone := make(chan struct{})
	lineCh := make(chan string, 8)
	go func() {
		defer close(readerDone)
		for {
			line, err := br.ReadBytes('\n')
			if err != nil {
				select {
				case lineCh <- "<err:" + err.Error() + ">":
				case <-readStopped:
				}
				return
			}
			select {
			case lineCh <- string(line):
			case <-readStopped:
				return
			}
		}
	}()

	sawDone := false
	deadline := time.After(5 * time.Second)
	for !sawDone {
		select {
		case l := <-lineCh:
			if strings.Contains(l, `"done"`) {
				sawDone = true
			}
		case <-deadline:
			t.Fatal("did not receive done line within 5s")
		}
	}

	// 3) 收尾（socketclient 的 defer）：必须秒级完成，不得永久阻塞。
	cleanupStart := time.Now()
	close(readStopped)
	_ = conn.SetReadDeadline(time.Now())
	select {
	case <-readerDone:
		// ok
	case <-time.After(3 * time.Second):
		t.Fatal("reader goroutine still blocked after cleanup — deadline no-op regression (chat done would never be logged)")
	}
	_ = conn.SetReadDeadline(time.Time{})
	if elapsed := time.Since(cleanupStart); elapsed > 2*time.Second {
		t.Fatalf("cleanup took %s, want <2s", elapsed)
	}
}
