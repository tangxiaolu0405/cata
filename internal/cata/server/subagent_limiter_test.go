package server

import (
	"context"
	"testing"
	"time"
)

func TestSubagentLimiterAcquireRelease(t *testing.T) {
	lim := newSubagentLimiter(2)
	ctx := context.Background()

	if err := lim.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := lim.acquire(ctx); err != nil {
		t.Fatal(err)
	}

	block := make(chan struct{})
	go func() {
		_ = lim.acquire(ctx)
		close(block)
	}()

	select {
	case <-block:
		t.Fatal("third acquire should block")
	case <-time.After(30 * time.Millisecond):
	}

	lim.release()
	select {
	case <-block:
	case <-time.After(2 * time.Second):
		t.Fatal("acquire did not unblock after release")
	}
}

func TestSubagentLimiterAcquireCancelled(t *testing.T) {
	lim := newSubagentLimiter(1)
	_ = lim.acquire(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- lim.acquire(ctx)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected cancel error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("acquire did not return after cancel")
	}
}
