package server

import (
	"sync/atomic"
	"testing"
	"time"
)

// 空闲回收语义：以「最后一条 chat 消息」为准（而不是客户端连接是否存在），
// 且**不再豁免 keep-alive**——注册到网关的工作空间同样休眠，由 supervisor
// （机器级隧道）/ 本地入口在下一条消息到达时按需拉起。

func newIdleTestServer(t *testing.T, opts Options) *Server {
	t.Helper()
	s, err := NewServerWithOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	return s
}

// stoppedWithin 启动 Wait 并返回是否在 d 内退出。
func stoppedWithin(s *Server, d time.Duration) bool {
	done := make(chan struct{})
	go func() {
		s.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// TestIdleReaperExitsWithoutMessages 无 chat 消息持续 idleTimeout 后进程应退出。
func TestIdleReaperExitsWithoutMessages(t *testing.T) {
	s := newIdleTestServer(t, Options{IdleTimeout: 300 * time.Millisecond})
	s.touchActivity()
	s.startIdleReaper()
	if !stoppedWithin(s, 3*time.Second) {
		t.Fatal("server did not exit after idle timeout")
	}
}

// TestIdleReaperAppliesToKeepAlive 回归：keep-alive（注册到网关）曾永久豁免空闲回收，
// 现在同样按消息空闲退出（网关侧消息由 supervisor 的机器级隧道兜住）。
func TestIdleReaperAppliesToKeepAlive(t *testing.T) {
	s := newIdleTestServer(t, Options{KeepAlive: true, IdleTimeout: 300 * time.Millisecond})
	s.touchActivity()
	s.startIdleReaper()
	if !stoppedWithin(s, 3*time.Second) {
		t.Fatal("keep-alive server should also exit when no message arrives")
	}
}

// TestIdleReaperDefersWhileStreaming 有流式轮次在执行（activeChatStreams>0）时不回收，
// 轮次结束后按剩余空闲时间收敛——避免长任务执行中被误杀。
func TestIdleReaperDefersWhileStreaming(t *testing.T) {
	s := newIdleTestServer(t, Options{IdleTimeout: 300 * time.Millisecond})
	s.touchActivity()
	atomic.AddInt32(&activeChatStreams, 1)
	t.Cleanup(func() { atomic.AddInt32(&activeChatStreams, -1) })
	s.startIdleReaper()

	if stoppedWithin(s, 900*time.Millisecond) {
		t.Fatal("server must not exit while a chat round is in flight")
	}
	atomic.AddInt32(&activeChatStreams, -1)
	if !stoppedWithin(s, 3*time.Second) {
		t.Fatal("server did not exit after the in-flight round finished")
	}
}

// TestIdleReaperDisabled idleTimeout=0 表示不空闲回收。
func TestIdleReaperDisabled(t *testing.T) {
	s := newIdleTestServer(t, Options{IdleTimeout: 0})
	s.touchActivity()
	s.startIdleReaper()
	if stoppedWithin(s, 700*time.Millisecond) {
		t.Fatal("idleTimeout=0 should never reap the server")
	}
}

// TestTouchActivityResetsIdle 新消息刷新计时：持续有消息的进程不退出。
func TestTouchActivityResetsIdle(t *testing.T) {
	s := newIdleTestServer(t, Options{IdleTimeout: 400 * time.Millisecond})
	s.touchActivity()
	s.startIdleReaper()
	for i := 0; i < 4; i++ {
		time.Sleep(150 * time.Millisecond)
		s.touchActivity()
		if stoppedWithin(s, 10*time.Millisecond) {
			t.Fatal("server exited despite steady message activity")
		}
	}
}
