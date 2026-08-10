package clock

import (
	"sync"
	"testing"
	"time"
)

// 回归：Location 在 loc==nil 时不得再次 Lock（曾导致子 Agent 测试永久阻塞）。
func TestLocationConcurrentInit(t *testing.T) {
	loc = nil
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l := Location(); l == nil {
				t.Error("nil location")
			}
			_ = Now()
			_ = RFC3339()
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock in clock.Location/Now")
	}
}
