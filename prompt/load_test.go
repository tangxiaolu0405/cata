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
	if !strings.Contains(s, "自主演进") || !strings.Contains(s, "consolidate") {
		t.Fatal("expected base + session compress extra")
	}
}

func TestEvolveSystemPrompt_includesPatchModes(t *testing.T) {
	s := EvolveSystemPrompt()
	if !strings.Contains(s, "replace_section") || !strings.Contains(s, "patch 模式选用") {
		t.Fatal("expected patch_modes.md merged into system prompt")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
