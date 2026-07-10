package server

import (
	"strings"
	"testing"

	"cata/internal/config"
)

func TestClampDelegateRounds(t *testing.T) {
	if clampDelegateRounds(0) != config.DefaultSubagentMaxRounds() {
		t.Fatalf("zero")
	}
	if clampDelegateRounds(99) != delegateMaxRoundsCap {
		t.Fatalf("cap")
	}
	if clampDelegateRounds(5) != 5 {
		t.Fatalf("mid")
	}
}

func TestFormatDelegateStartedContainsID(t *testing.T) {
	msg := formatDelegateStarted("sub-3", "worker-model", 4, 7, "/tmp/proj")
	for _, part := range []string{"sub-3", "worker-model", "max_concurrent=4", "tools=7", "subagent_runs"} {
		if !strings.Contains(msg, part) {
			t.Fatalf("missing %q in %q", part, msg)
		}
	}
}

func TestBuildWorkerToolsIncludesReadSkillAndRunCommand(t *testing.T) {
	cfg := &config.AppConfig{}
	cfg.Exec.Enabled = true
	config.Config = cfg
	t.Cleanup(func() { config.Config = nil })

	ss := &SocketServer{}
	reg := NewToolRegistry()
	ss.RegisterBuiltinTools(reg)
	ss.tools = reg

	names := map[string]bool{}
	for _, tool := range ss.buildWorkerTools() {
		names[tool.Function.Name] = true
	}
	for _, want := range []string{
		"read_skill", "run_command", "run_skill",
		"search_replace", "append_file", "create_file",
	} {
		if !names[want] {
			t.Fatalf("worker tools missing %q: %v", want, names)
		}
	}
	for _, excluded := range []string{"ask_user", "delegate_task", "delegate_wait"} {
		if names[excluded] {
			t.Fatalf("worker tools must not include %q", excluded)
		}
	}
}

func TestMaybeAppendDelegateHints(t *testing.T) {
	started := formatDelegateStarted("sub-1", "m", 4, 3, "/tmp")
	got := maybeAppendDelegateHints(started, strings.Repeat("x", 3000), "")
	if !strings.Contains(got, "context empty") || !strings.Contains(got, "very long") {
		t.Fatalf("got %q", got)
	}
}
