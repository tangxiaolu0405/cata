package server

import (
	"context"
	"io"
	"os"
)

const ctxReadChunkSize = 64 * 1024

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// readFileWithContext reads up to maxBytes, checking ctx between chunks.
func readFileWithContext(ctx context.Context, path string, maxBytes int) ([]byte, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if maxBytes <= 0 {
		maxBytes = 512 * 1024
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	buf := make([]byte, 0, minInt(ctxReadChunkSize, maxBytes))
	chunk := make([]byte, ctxReadChunkSize)
	for len(buf) < maxBytes {
		if err := contextErr(ctx); err != nil {
			return nil, err
		}
		n, err := f.Read(chunk)
		if n > 0 {
			remain := maxBytes - len(buf)
			if n > remain {
				n = remain
			}
			buf = append(buf, chunk[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return buf, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
