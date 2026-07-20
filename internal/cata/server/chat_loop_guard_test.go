package server

import (
	"testing"

	"cata/internal/cata/brain"
)

func TestChatLoopGuardUsesTaskLimitsNotGlobals(t *testing.T) {
	task := &brain.TaskState{MaxToolRounds: 3}
	g := newChatLoopGuard(task)
	g.hardMaxRounds = 200
	if g.checkBudget(3) != nil {
		t.Fatal("round 3 within task budget")
	}
	brk := g.checkBudget(4)
	if brk == nil || brk.Code != FailCodeBudgetExhausted {
		t.Fatalf("got %+v", brk)
	}
}

func TestChatLoopGuardHardCeilingWhenNoTaskLimit(t *testing.T) {
	g := newChatLoopGuard(nil)
	g.hardMaxRounds = 5
	if g.checkBudget(5) != nil {
		t.Fatal("at ceiling ok")
	}
	if g.checkBudget(6) == nil {
		t.Fatal("want hard ceiling break")
	}
}

func TestChatLoopGuardConsecutiveOffUntilDeclared(t *testing.T) {
	g := newChatLoopGuard(&brain.TaskState{}) // limits 0 = off
	fail := []chatToolExecResult{{name: "run_command", out: "[error] boom"}}
	for i := 0; i < 20; i++ {
		if brk := g.observe(fail); brk != nil {
			t.Fatalf("consec should be off without task limit: %v", brk)
		}
	}
}

func TestChatLoopGuardConsecutiveTaskLimit(t *testing.T) {
	g := newChatLoopGuard(&brain.TaskState{MaxConsecutiveFailures: 2})
	fail := []chatToolExecResult{{name: "run_command", out: "[error] boom"}}
	if g.observe(fail) != nil {
		t.Fatal("1st")
	}
	brk := g.observe(fail)
	if brk == nil || brk.Code != FailCodeConsecutiveFailures {
		t.Fatalf("got %+v", brk)
	}
}

func TestChatLoopGuardNoProgressTaskLimit(t *testing.T) {
	g := newChatLoopGuard(&brain.TaskState{MaxStaleRounds: 2})
	ok := []chatToolExecResult{{name: "read_file", out: "same"}}
	_ = g.observe(ok)
	_ = g.observe(ok)
	brk := g.observe(ok)
	if brk == nil || brk.Code != FailCodeNoProgress {
		t.Fatalf("got %+v", brk)
	}
}

func TestToolResultLooksFailed(t *testing.T) {
	if !toolResultLooksFailed("[error] no") {
		t.Fatal("error tag")
	}
	if toolResultLooksFailed("run_skill ok\nhello") {
		t.Fatal("ok should pass")
	}
}
