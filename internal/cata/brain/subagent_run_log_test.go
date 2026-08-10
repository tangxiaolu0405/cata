package brain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cata/internal/cata/config"
)

func TestSubagentCSVFileName(t *testing.T) {
	cases := []struct {
		cwd  string
		want string
	}{
		{"/home/user/proj", "home_user_proj.csv"},
		{"/tmp/my-project", "tmp_my-project.csv"},
		{"", "_no_cwd.csv"},
	}
	for _, tc := range cases {
		got := subagentCSVFileName(tc.cwd)
		if got != tc.want {
			t.Fatalf("cwd %q: got %q want %q", tc.cwd, got, tc.want)
		}
	}
}

func TestAppendSubagentRunCSVPerOutputCwd(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)

	rec := SubagentRunRecord{
		SessionID:     "cs-test",
		DelegateIndex: 1,
		StartedAt:     "2026-06-07T10:00:00+08:00",
		FinishedAt:    "2026-06-07T10:01:00+08:00",
		ID:            "sub-1",
		OutputCwd:     "/tmp/my-project",
		Model:         "worker-model",
		Status:        "ok",
		Rounds:        1,
		Task:          "t",
		Summary:       "ok",
	}
	if err := AppendSubagentRunCSV(rec); err != nil {
		t.Fatal(err)
	}

	path := SubagentRunsCSVPath("/tmp/my-project")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, subagentCSVHeader) || !strings.Contains(text, "sub-1") {
		t.Fatalf("bad csv at %s: %q", path, text)
	}
	if _, err := os.Stat(filepath.Join(home, DirSubagentRuns, "_no_cwd.csv")); err == nil {
		t.Fatal("should not create _no_cwd file")
	}
}

func TestCapSubagentField(t *testing.T) {
	long := strings.Repeat("x", maxSubagentField+10)
	got := capSubagentField(long)
	if len(got) != maxSubagentField+len("…") {
		t.Fatalf("len=%d", len(got))
	}
}
