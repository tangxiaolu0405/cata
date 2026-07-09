package server

import "context"

// subagentLimiter caps concurrent worker goroutines (channel semaphore).
type subagentLimiter struct {
	slots chan struct{}
}

func newSubagentLimiter(max int) *subagentLimiter {
	if max <= 0 {
		max = 4
	}
	ch := make(chan struct{}, max)
	for i := 0; i < max; i++ {
		ch <- struct{}{}
	}
	return &subagentLimiter{slots: ch}
}

func (l *subagentLimiter) acquire(ctx context.Context) error {
	if l == nil {
		return nil
	}
	select {
	case <-l.slots:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *subagentLimiter) release() {
	if l == nil {
		return
	}
	select {
	case l.slots <- struct{}{}:
	default:
	}
}

func (l *subagentLimiter) capacity() int {
	if l == nil {
		return 0
	}
	return cap(l.slots)
}

func (l *subagentLimiter) runningCount() int {
	if l == nil {
		return 0
	}
	return cap(l.slots) - len(l.slots)
}

func (l *subagentLimiter) isFull() bool {
	if l == nil {
		return false
	}
	return len(l.slots) == 0
}
