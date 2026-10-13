package tunnel

import (
	"errors"
	"net"
	"testing"
	"time"
)

// newTestStream 构造一个不依赖 WebSocket 的 streamConn（仅测读侧语义）。
// agent 为 nil 时不得走 Write 路径。
func newTestStream() *streamConn {
	return &streamConn{
		id:        1,
		rch:       make(chan []byte, 64),
		errCh:     make(chan error, 1),
		opened:    make(chan struct{}),
		done:      make(chan struct{}),
		createdAt: time.Now(),
	}
}

// TestStreamConnReadDeadlineWakesBlockedRead 验证 A 的核心修复：
// 阻塞中的 Read 在 SetReadDeadline(now) 后必须立即返回超时错误，
// 而不是永久阻塞（旧实现 SetReadDeadline 是 no-op，导致 socketclient
// chat 收尾的 <-readerDone 永久等待、永不打印 chat done）。
func TestStreamConnReadDeadlineWakesBlockedRead(t *testing.T) {
	sc := newTestStream()

	type res struct {
		n   int
		err error
	}
	done := make(chan res, 1)
	go func() {
		buf := make([]byte, 16)
		n, err := sc.Read(buf)
		done <- res{n, err}
	}()

	// 让 Read 先进入阻塞（rch 空、流未关）。
	time.Sleep(50 * time.Millisecond)

	// 模拟 socketclient 收尾：SetReadDeadline(now) 打断阻塞读。
	if err := sc.SetReadDeadline(time.Now()); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}

	select {
	case r := <-done:
		if r.err == nil {
			t.Fatalf("expected timeout error, got nil (n=%d)", r.n)
		}
		var netErr net.Error
		if !errors.As(r.err, &netErr) || !netErr.Timeout() {
			t.Fatalf("expected net.Error.Timeout()==true, got %v", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read still blocked after SetReadDeadline(now) — regression of the deadline no-op bug")
	}
}

// TestStreamConnReadDeadlineFuture 验证未来 deadline 到期后 Read 返回超时。
func TestStreamConnReadDeadlineFuture(t *testing.T) {
	sc := newTestStream()
	if err := sc.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	buf := make([]byte, 16)
	_, err := sc.Read(buf)
	elapsed := time.Since(start)

	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("expected timeout error, got %v", err)
	}
	if elapsed < 50*time.Millisecond || elapsed > time.Second {
		t.Fatalf("unexpected wait %s (want ~100ms)", elapsed)
	}
}

// TestStreamConnReadDeadlineCleared 验证清除 deadline 后 Read 恢复永久阻塞
// （不会因残留 deadline 而误超时），且数据到达时正常返回。
func TestStreamConnReadDeadlineCleared(t *testing.T) {
	sc := newTestStream()
	if err := sc.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	// 清除 deadline（socketclient 读完一行后会 SetReadDeadline(time.Time{})）。
	if err := sc.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}

	// 清除后写入数据，Read 应正常返回数据而非超时。
	go func() {
		time.Sleep(30 * time.Millisecond)
		sc.rch <- []byte("hello\n")
	}()

	buf := make([]byte, 16)
	n, err := sc.Read(buf)
	if err != nil {
		t.Fatalf("expected data, got err %v", err)
	}
	if got := string(buf[:n]); got != "hello\n" {
		t.Fatalf("got %q, want %q", got, "hello\n")
	}
}

// TestStreamConnReadDeadlineDataWins 验证 deadline 与数据同时就绪时数据优先
// （不因超时分支抢占而丢数据）。
func TestStreamConnReadDeadlineDataWins(t *testing.T) {
	sc := newTestStream()
	sc.rch <- []byte("data\n")
	if err := sc.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 16)
	n, err := sc.Read(buf)
	if err != nil {
		t.Fatalf("expected data, got err %v", err)
	}
	if got := string(buf[:n]); got != "data\n" {
		t.Fatalf("got %q, want %q", got, "data\n")
	}
}

// TestStreamConnReaderStalled 验证 C：读侧长时间未消费被判为「卡死」，
// 而正常消费中的流不被判为卡死。
func TestStreamConnReaderStalled(t *testing.T) {
	// 从未消费 + 流创建已久 → 卡死。
	old := newTestStream()
	old.createdAt = time.Now().Add(-2 * readerStalledGrace)
	if !old.readerStalled() {
		t.Fatal("stream never consumed and older than grace should be stalled")
	}

	// 从未消费 + 刚创建 → 不算卡死（避免新流误判）。
	fresh := newTestStream()
	if fresh.readerStalled() {
		t.Fatal("fresh stream should not be considered stalled")
	}

	// 最近消费过 → 不算卡死（慢消费者，交给配额断流）。
	active := newTestStream()
	active.lastReadNano.Store(time.Now().UnixNano())
	if active.readerStalled() {
		t.Fatal("recently-read stream should not be considered stalled")
	}

	// 消费过但已静默超阈值 → 卡死。
	stale := newTestStream()
	stale.lastReadNano.Store(time.Now().Add(-2 * readerStalledGrace).UnixNano())
	if !stale.readerStalled() {
		t.Fatal("stream with stale read time should be stalled")
	}
}
