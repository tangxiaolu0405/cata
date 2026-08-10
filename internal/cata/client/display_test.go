package client

import (
	"strings"
	"testing"
)

func TestParseChatOptions(t *testing.T) {
	opts := ParseChatOptions([]string{"--dir", "/tmp/a", "--quiet", "--dir", "/tmp/b"})
	if len(opts.Dirs) != 2 || opts.Dirs[0] != "/tmp/a" || opts.Dirs[1] != "/tmp/b" {
		t.Fatalf("dirs = %v, want 2 dirs", opts.Dirs)
	}
	if opts.displayMode() != "quiet" {
		t.Fatalf("displayMode = %q, want quiet", opts.displayMode())
	}
	if opts.firstDir() != "/tmp/a" {
		t.Fatalf("firstDir = %q, want /tmp/a", opts.firstDir())
	}
}

func TestParseChatOptionsVerbose(t *testing.T) {
	opts := ParseChatOptions([]string{"-v"})
	if opts.displayMode() != "verbose" {
		t.Fatalf("displayMode = %q, want verbose", opts.displayMode())
	}
}

func TestParseChatOptionsDefault(t *testing.T) {
	opts := ParseChatOptions(nil)
	if opts.displayMode() != "" {
		t.Fatalf("displayMode = %q, want empty", opts.displayMode())
	}
}

func TestFormatToolResultLineLevels(t *testing.T) {
	ev := map[string]any{
		"name": "read_file", "output": "some contents", "level": "silent",
	}
	// auto 模式：silent 级不显示正文
	if got := formatToolResultLine("tool_result", ev, ""); got != "" {
		t.Fatalf("auto+silent = %q, want empty", got)
	}
	// verbose 模式：silent 级仍显示（用户要求完整）
	if got := formatToolResultLine("tool_result", ev, "verbose"); !strings.Contains(got, "some contents") {
		t.Fatalf("verbose+silent = %q, want contents", got)
	}

	ev2 := map[string]any{"name": "search_replace", "output": "ok", "level": "normal"}
	if got := formatToolResultLine("tool_result", ev2, ""); !strings.Contains(got, "ok") {
		t.Fatalf("auto+normal = %q, want ok", got)
	}

	ev3 := map[string]any{"name": "run_command", "output": strings.Repeat("x", 500), "level": "verbose"}
	// auto+verbose：长输出截断到 2000
	if got := formatToolResultLine("tool_result", ev3, ""); len(got) > 2100 {
		t.Fatalf("auto+verbose truncated length too long: %d", len(got))
	}
	// verbose 模式完整展示
	if got := formatToolResultLine("tool_result", ev3, "verbose"); len(got) > 2100 {
		t.Fatalf("verbose truncated length too long: %d", len(got))
	}
}

func TestFormatToolResultLineEmpty(t *testing.T) {
	ev := map[string]any{"name": "read_file", "output": "", "level": "normal"}
	if got := formatToolResultLine("tool_result", ev, ""); !strings.Contains(got, "done") {
		t.Fatalf("empty output = %q, want done marker", got)
	}
}
