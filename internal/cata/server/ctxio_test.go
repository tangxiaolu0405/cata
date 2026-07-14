package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadFileWithContextCancelled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	data := make([]byte, 256*1024)
	for i := range data {
		data[i] = 'x'
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = readFileWithContext(ctx, path, len(data))
	}()

	time.Sleep(5 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("read did not stop after cancel")
	}

	if err := contextErr(ctx); err == nil {
		t.Fatal("expected cancelled context")
	}
}
