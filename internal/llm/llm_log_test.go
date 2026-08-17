package llm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAppendLLMLogCreatesDir 验证 llm/<sanitized>.log 目录不存在时自动创建
// （os.O_CREATE 不建父目录，此前按产出区拆分日志会静默写失败，命中观测数据丢失）。
func TestAppendLLMLogCreatesDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	nested := filepath.Join(home, "nested", "llm.log")
	t.Setenv("LLM_LOG_FILE", nested)

	c := &Client{apiURL: "http://example.com", model: "m"}
	c.appendLLMLog(ChatRequest{}, nil, "", "hello reply", nil, nil)

	data, err := os.ReadFile(nested)
	if err != nil {
		t.Fatalf("llm log should be created with parent dir: %v", err)
	}
	if !strings.Contains(string(data), "hello reply") {
		t.Fatalf("log missing content: %s", string(data))
	}
}

// TestAppendLLMLogRecordsRetrieved 验证命中观测字段写入 llm.log。
func TestAppendLLMLogRecordsRetrieved(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	logFile := filepath.Join(home, "llm.log")
	t.Setenv("LLM_LOG_FILE", logFile)

	c := &Client{apiURL: "http://example.com", model: "m", lastRetrieved: []string{"memory/long/foo.md"}}
	c.appendLLMLog(ChatRequest{}, nil, "", "reply", nil, nil)

	data, _ := os.ReadFile(logFile)
	if !strings.Contains(string(data), "memory/long/foo.md") {
		t.Fatalf("retrieved_memory missing from log: %s", string(data))
	}
}
