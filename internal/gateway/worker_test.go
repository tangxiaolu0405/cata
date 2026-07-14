package gateway

import (
	"path/filepath"
	"testing"
)

func TestWorkerCwd(t *testing.T) {
	dir, err := WorkerCwd("/tmp/cata_worker", "telegram", "12345")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/tmp/cata_worker", "telegram", "12345")
	if dir != want {
		t.Fatalf("got %q want %q", dir, want)
	}
}

func TestWorkerCwd_sanitize(t *testing.T) {
	dir, err := WorkerCwd("/tmp/w", "telegram", "bad/id")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "bad_id" {
		t.Fatalf("sanitized base=%q", filepath.Base(dir))
	}
}

func TestWorkerCwdForSession(t *testing.T) {
	root := t.TempDir()
	dir, err := WorkerCwdForSession(root, SessionKeyFor("telegram", "99"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WorkerCwdForSession(root, "badkey"); err == nil {
		t.Fatal("expected error for bad session key")
	}
	if dir != filepath.Join(root, "telegram", "99") {
		t.Fatalf("got %q", dir)
	}
}

func TestWorkerRoot_default(t *testing.T) {
	r := WorkerRoot("")
	if r == "" {
		t.Fatal("empty worker root")
	}
}
