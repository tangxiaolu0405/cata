package server

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestSubagentPoolWaitManyViaChannel(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ss := &SocketServer{}
	pool := newSubagentPool(ctx, ss, server)

	st1 := &subagentTask{id: "sub-1", done: make(chan struct{})}
	st2 := &subagentTask{id: "sub-2", done: make(chan struct{})}
	pool.mu.Lock()
	pool.tasks[st1.id] = st1
	pool.tasks[st2.id] = st2
	pool.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		done <- pool.waitMany(ctx, []*subagentTask{st1, st2})
	}()

	time.Sleep(20 * time.Millisecond)
	close(st1.done)
	time.Sleep(10 * time.Millisecond)
	close(st2.done)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("waitMany: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waitMany did not return")
	}
}

func TestSubagentPoolCancelAll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pool := newSubagentPool(ctx, &SocketServer{}, nil)

	taskCtx, taskCancel := context.WithCancel(context.Background())
	st := &subagentTask{
		id: "sub-1", ctx: taskCtx, cancel: taskCancel, done: make(chan struct{}),
	}
	pool.mu.Lock()
	pool.tasks[st.id] = st
	pool.mu.Unlock()

	cancel()
	time.Sleep(20 * time.Millisecond)

	select {
	case <-taskCtx.Done():
	default:
		t.Fatal("expected child context cancelled when parent cancelled")
	}
}
