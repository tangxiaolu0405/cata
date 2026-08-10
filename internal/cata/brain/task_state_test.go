package brain

import (
	"os"
	"path/filepath"
	"sync"
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
		Goal:           "ship feature",
		Acceptance:     []string{"tests green"},
		Steps:          []string{"write", "test"},
		SetAcceptance:  true,
		SetSteps:       true,
		MaxToolRounds:  intPtr(12),
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

// 回归：同一毫秒内并发创建任务不得产生重复 ID（曾因时间+毫秒后缀碰撞导致 flaky）。
func TestNewTaskIDUniqueConcurrent(t *testing.T) {
	const n = 200
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids[i] = newTaskID()
		}(i)
	}
	wg.Wait()
	seen := make(map[string]struct{}, n)
	for _, id := range ids {
		if id == "" {
			t.Fatal("empty task id")
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate task id: %s", id)
		}
		seen[id] = struct{}{}
	}
}

// randHex 输出指定长度的十六进制字符。
func TestRandHexLength(t *testing.T) {
	if got := randHex(4); len(got) != 4 {
		t.Fatalf("randHex(4) = %q, want length 4", got)
	}
	if got := randHex(6); len(got) != 6 {
		t.Fatalf("randHex(6) = %q, want length 6", got)
	}
}
