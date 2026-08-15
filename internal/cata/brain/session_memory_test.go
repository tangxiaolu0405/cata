package brain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cata/internal/cata/config"
)

func TestFinalizeShortTermAfterConsolidate_writesArchive(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)
	ws := &Workspace{ID: "ws-arch", RootPath: home, ActiveMode: ModeDefaultID}
	if err := os.MkdirAll(ws.Dir(), 0755); err != nil {
		t.Fatal(err)
	}
	short := ws.ShortTermPath()
	if err := os.MkdirAll(filepath.Dir(short), 0755); err != nil {
		t.Fatal(err)
	}
	body := shortTermFileHeader + strings.Repeat("x", 400)
	if err := os.WriteFile(short, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	SetActive(ws)
	defer SetActive(nil)

	rel, err := FinalizeShortTermAfterConsolidate(ws, 512)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rel, RelMemoryArchive+"/consolidated-") {
		t.Fatalf("want archive path, got %q", rel)
	}
	if _, err := os.Stat(ws.Path(rel)); err != nil {
		t.Fatalf("archive file missing: %v", err)
	}
	longDir := ws.LongTermDir()
	entries, _ := os.ReadDir(longDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "consolidated-") {
			t.Fatalf("consolidated should not be under long/: %s", e.Name())
		}
	}
	after, err := os.ReadFile(short)
	if err != nil || !strings.Contains(string(after), "Last consolidated") {
		t.Fatalf("short-term not reset: %v %q", err, string(after))
	}
}
