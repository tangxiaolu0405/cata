package brain

import (
	"strings"
	"testing"
)

func TestEnrichWorkerDelegateContext(t *testing.T) {
	SetOutputCwd(`D:\stock`)
	defer SetOutputCwd("")
	SetRuntimeEnv(&RuntimeEnv{Shell: "cmd", Terminal: "cmd"})
	defer SetRuntimeEnv(nil)

	out := EnrichWorkerDelegateContext("raw=data/rows.json")
	if !strings.Contains(out, `D:\stock`) {
		t.Fatalf("missing cwd: %q", out)
	}
	if !strings.Contains(out, "parent context") || !strings.Contains(out, "rows.json") {
		t.Fatalf("missing parent: %q", out)
	}
	if !strings.Contains(out, "Windows-native") {
		t.Fatalf("missing path hint: %q", out)
	}
}

func TestLoadDelegateGuideFromEmbed(t *testing.T) {
	block := RenderDelegateGuideBlock()
	if !strings.Contains(block, "minimal") && !strings.Contains(block, "context") {
		t.Fatalf("block=%q", block[:min(80, len(block))])
	}
}

func TestLoadMinimalBootFromEmbed(t *testing.T) {
	if !strings.Contains(LoadMinimalBootPrompt(), "Cata") {
		t.Fatal("empty minimal boot")
	}
}

func TestLoadDelegateTaskToolSpec(t *testing.T) {
	spec, err := LoadDelegateTaskToolSpec()
	if err != nil {
		t.Fatal(err)
	}
	if spec.Description == "" || len(spec.Parameters) == 0 {
		t.Fatal("empty spec")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
