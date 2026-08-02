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

func TestTerminalBrainExtensionTaskIncludesPersonaSkipsGuidance(t *testing.T) {
	SetPromptProfile(PromptProfileTask)
	defer ClearPromptProfile()

	ext := TerminalBrainSystemExtension(3000, 8000)
	if strings.Contains(ext, TerminalGuidanceSystemPrefix) {
		t.Fatal("task should skip global guidance")
	}
	// task 档需注入 active mode，否则首轮看不到项目 SOP / 委派路由
	if !strings.Contains(ext, TerminalProjectContentSystemPrefix) {
		t.Fatal("task should include project persona/behavior")
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
	if ProfileRank(PromptProfileMinimal) != 0 {
		t.Fatal("minimal should rank lowest")
	}
	if ProfileRank(PromptProfileTask) != 1 || ProfileRank(PromptProfileFull) != 2 {
		t.Fatal("rank order")
	}
}
