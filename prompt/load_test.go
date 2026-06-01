package prompt

import (
	"strings"
	"testing"
)

func TestLoad_embeddedEvolveSystem(t *testing.T) {
	s := EvolveSystemPrompt()
	if !strings.Contains(s, "自主演进") {
		t.Fatalf("expected evolve system prompt, got %q", s[:min(80, len(s))])
	}
}

func TestEvolveSessionCompressPrompt_composes(t *testing.T) {
	s := EvolveSessionCompressPrompt()
	if !strings.Contains(s, "自主演进") || !strings.Contains(s, "对话轮次阈值") {
		t.Fatal("expected base + session compress extra")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
