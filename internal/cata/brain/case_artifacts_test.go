package brain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaseArtifactRoundTrip(t *testing.T) {
	cwd := t.TempDir()
	v, path, err := WriteCaseArtifact(cwd, "feat1", "spec", "# Spec\n\nok\n", "architect")
	if err != nil {
		t.Fatal(err)
	}
	if v != 1 || path == "" {
		t.Fatalf("v=%d path=%q", v, path)
	}
	if err := SetCaseArtifactStatus(cwd, "feat1", "spec", ArtifactAccepted, "_default", ""); err != nil {
		t.Fatal(err)
	}
	body, meta, err := ReadCaseArtifact(cwd, "feat1", "spec", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "ok") || !strings.Contains(meta, "accepted") {
		t.Fatalf("body=%q meta=%q", body, meta)
	}
	logPath, err := AppendModeRunLog(cwd, ModeRunLog{
		ModeID: "coder", CaseID: "feat1", SubagentID: "sub-1",
		Task: "impl", Status: "ok", Summary: "done", Rounds: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Fatal(err)
	}
	wantSub := filepath.Join("cases", "feat1", "mode_runs", "coder")
	if !strings.Contains(logPath, wantSub) {
		t.Fatalf("log path %q want contain %q", logPath, wantSub)
	}
}

func TestListProjectModes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	root := t.TempDir()
	ws := &Workspace{ID: "ws-modes", RootPath: root, ActiveMode: ModeDefaultID}
	if err := ws.EnsureScaffold(); err != nil {
		t.Fatal(err)
	}
	modes, err := ListProjectModes(ws)
	if err != nil {
		t.Fatal(err)
	}
	foundOrch, foundCoder := false, false
	for _, m := range modes {
		if m.ID == ModeDefaultID {
			foundOrch = true
		}
		if m.ID == "coder" {
			foundCoder = true
		}
	}
	if !foundOrch {
		t.Fatalf("expected _default, modes=%v", modes)
	}
	if foundCoder {
		t.Fatalf("scaffold must not seed coder: %v", modes)
	}
}
