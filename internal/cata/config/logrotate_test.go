package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTruncateLogKeepTail(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.log")
	// 构造 3MB 日志：2MB 头部填充 'a' + 尾部一行标记。
	head := strings.Repeat("a", int(maxProcessLogBytes)+100)
	tail := "tail-marker-line\n"
	if err := os.WriteFile(p, []byte(head+tail), 0644); err != nil {
		t.Fatal(err)
	}

	truncateLogKeepTail(p)

	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() > keepProcessLogTail+1024 {
		t.Fatalf("log not truncated enough: size=%d", st.Size())
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), "tail-marker-line") {
		t.Fatalf("tail lost after truncate: %q", string(data[:min(len(data), 200)]))
	}
}

func TestTruncateLogKeepTail_underThreshold(t *testing.T) {
	p := filepath.Join(t.TempDir(), "small.log")
	if err := os.WriteFile(p, []byte("small log line\n"), 0644); err != nil {
		t.Fatal(err)
	}
	truncateLogKeepTail(p)
	data, _ := os.ReadFile(p)
	if string(data) != "small log line\n" {
		t.Fatalf("small log should be untouched: %q", string(data))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
