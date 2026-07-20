package brain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBeginOrResumeAndFailRecover(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	root := t.TempDir()
	w := &Workspace{ID: "ws1", RootPath: root, ActiveMode: "default"}
	if err := os.MkdirAll(w.Dir(), 0755); err != nil {
		t.Fatal(err)
	}

	st, resumed, err := BeginOrResumeTask(w, "fix the bug", root)
	if err != nil || resumed || st.Status != TaskStatusRunning {
		t.Fatalf("start: st=%+v resumed=%v err=%v", st, resumed, err)
	}

	if err := MarkTaskFailed(w, st, "budget_exhausted", "budget", 3, 0, 0, "run_command", "fp"); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCurrentTask(w)
	if err != nil || loaded.Status != TaskStatusFailed {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}

	st2, resumed2, err := BeginOrResumeTask(w, "继续", root)
	if err != nil || !resumed2 || st2.Status != TaskStatusRunning {
		t.Fatalf("resume: st=%+v resumed=%v err=%v", st2, resumed2, err)
	}
	if st2.ID != st.ID {
		t.Fatalf("id changed %s -> %s", st.ID, st2.ID)
	}

	if err := MarkTaskFailed(w, st2, "no_progress", "stuck", 5, 0, 4, "read_file", "fp2"); err != nil {
		t.Fatal(err)
	}
	st3, resumed3, err := BeginOrResumeTask(w, "全新任务", root)
	if err != nil || resumed3 {
		t.Fatalf("new: resumed=%v err=%v", resumed3, err)
	}
	if st3.ID == st.ID {
		t.Fatal("expected new task id after non-continue message on failed task")
	}
}

func TestUpdateTaskContractAndClear(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	root := t.TempDir()
	w := &Workspace{ID: "ws2", RootPath: root, ActiveMode: "default"}
	if err := os.MkdirAll(w.Dir(), 0755); err != nil {
		t.Fatal(err)
	}
	st, err := UpdateTaskContract(w, TaskContract{
		Goal:          "ship feature",
		Acceptance:    []string{"tests green"},
		Steps:         []string{"write", "test"},
		SetAcceptance: true,
		SetSteps:      true,
		MaxToolRounds: intPtr(12),
		MaxStaleRounds: intPtr(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Acceptance) != 1 || st.Goal != "ship feature" || st.MaxToolRounds != 12 || st.MaxStaleRounds != 3 {
		t.Fatalf("%+v", st)
	}
	if err := ClearCurrentTask(w); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.CurrentTaskPath()); !os.IsNotExist(err) {
		t.Fatalf("current should be gone: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(w.TaskDir(), "archive"))
	if len(entries) == 0 {
		t.Fatal("expected archive")
	}
}

func intPtr(n int) *int { return &n }
