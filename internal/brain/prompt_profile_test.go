package brain

import (
	"strings"
	"testing"
)

func TestTerminalPathsMinimalOmitsHeavyBlocks(t *testing.T) {
	SetPromptProfile(PromptProfileMinimal)
	defer ClearPromptProfile()

	block := TerminalPathsSystemBlock()
	if !strings.Contains(block, "tools[]") {
		t.Fatal("expected tools[] hint")
	}
	if strings.Contains(block, "delegate_task") {
		t.Fatal("minimal block should omit subagent guide")
	}
	if strings.Contains(block, "Cata 已注册工具") {
		t.Fatal("minimal block should not list registered tools")
	}
}

func TestTerminalPathsTaskOmitsSubagent(t *testing.T) {
	SetPromptProfile(PromptProfileTask)
	defer ClearPromptProfile()

	block := TerminalPathsSystemBlock()
	if strings.Contains(block, "delegate_task") {
		t.Fatal("task block should omit subagent guide")
	}
	if !strings.Contains(block, "run_command") {
		t.Fatal("task block should mention run_command")
	}
}

func TestTerminalBrainExtensionMinimalSkipsDocs(t *testing.T) {
	SetPromptProfile(PromptProfileMinimal)
	defer ClearPromptProfile()

	ext := TerminalBrainSystemExtension(800, 2000)
	if strings.Contains(ext, TerminalGuidanceSystemPrefix) {
		t.Fatal("minimal should skip guidance")
	}
	if strings.Contains(ext, TerminalProjectContentSystemPrefix) {
		t.Fatal("minimal should skip project content")
	}
}

func TestTerminalBrainExtensionTaskSkipsPersona(t *testing.T) {
	SetPromptProfile(PromptProfileTask)
	defer ClearPromptProfile()

	ext := TerminalBrainSystemExtension(3000, 8000)
	if strings.Contains(ext, TerminalGuidanceSystemPrefix) {
		t.Fatal("task should skip global guidance")
	}
	if strings.Contains(ext, TerminalProjectContentSystemPrefix) {
		t.Fatal("task should skip project persona")
	}
}

func TestPromptProfileMaxSticky(t *testing.T) {
	if got := PromptProfileMax(PromptProfileMinimal, PromptProfileTask); got != PromptProfileTask {
		t.Fatalf("got %v", got)
	}
	if got := PromptProfileMax(PromptProfileTask, PromptProfileFull); got != PromptProfileFull {
		t.Fatalf("got %v", got)
	}
	if got := PromptProfileMax(PromptProfileFull, PromptProfileMinimal); got != PromptProfileFull {
		t.Fatalf("got %v", got)
	}
}

func TestProfileRank(t *testing.T) {
	if ProfileRank(PromptProfileLight) != 0 {
		t.Fatal("light alias should rank as minimal")
	}
	if ProfileRank(PromptProfileTask) != 1 || ProfileRank(PromptProfileFull) != 2 {
		t.Fatal("rank order")
	}
}
