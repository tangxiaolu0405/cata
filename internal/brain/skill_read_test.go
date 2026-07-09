package brain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cata/internal/config"
)

func TestReadSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)
	skillDir := filepath.Join(home, DirSkills, "demo-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	body := "# Demo Skill\n\nFollow these steps carefully."
	skillPath := filepath.Join(skillDir, FileSkillMD)
	if err := os.WriteFile(skillPath, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := ReadSkill(context.Background(), ReadSkillArgs{Skill: "demo-skill"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "resolved="+skillPath) {
		t.Fatalf("missing resolved path: %q", out)
	}
	if !strings.Contains(out, "Follow these steps carefully.") {
		t.Fatalf("missing body: %q", out)
	}
}

func TestReadSkillRequiresID(t *testing.T) {
	_, err := ReadSkill(context.Background(), ReadSkillArgs{})
	if err == nil || !strings.Contains(err.Error(), "skill required") {
		t.Fatalf("got %v", err)
	}
}

func TestReadSkillNotFound(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)
	_, err := ReadSkill(context.Background(), ReadSkillArgs{Skill: "missing"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got %v", err)
	}
}

func TestReadSkillTruncation(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)
	skillDir := filepath.Join(home, DirSkills, "big-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.AppConfig{}
	cfg.WorkspaceFiles.MaxReadBytes = 32
	config.Config = cfg
	t.Cleanup(func() { config.Config = nil })

	if err := os.WriteFile(filepath.Join(skillDir, FileSkillMD), []byte(strings.Repeat("x", 64)), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := ReadSkill(context.Background(), ReadSkillArgs{Skill: "big-skill"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "truncated by max_read_bytes") {
		t.Fatalf("expected truncation marker: %q", out)
	}
}
