package server

import (
	"testing"
)

// TestServerStopConcurrentIdempotent 多个 goroutine 同时调 Stop 只执行一次清理
// （心跳兜底 / 空闲回收 / SIGTERM 可能并发触发）：不 panic、重复调用安全。
func TestServerStopConcurrentIdempotent(t *testing.T) {
	s, err := NewServer(false)
	if err != nil {
		t.Fatal(err)
	}
	// 多 goroutine 并发 Stop（含重复调用）。
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Stop panic: %v", r)
				}
				done <- struct{}{}
			}()
			s.Stop()
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	// 串行再停一次也应安全（once 已触发）。
	s.Stop()
}

// TestServerStopTriggersCancellation Stop 后 Wait 应返回（ctx 取消）。
func TestServerStopTriggersCancellation(t *testing.T) {
	s, err := NewServer(false)
	if err != nil {
		t.Fatal(err)
	}
	waitDone := make(chan struct{})
	go func() { s.Wait(); close(waitDone) }()
	s.Stop()
	select {
	case <-waitDone:
		// ok
	default:
		t.Fatal("Wait did not return after Stop")
	}
}
