package llm

import (
	"os"
	"strings"
	"testing"

	"cata/internal/cata/brain"
)

func TestParseRoleCard(t *testing.T) {
	raw := "---\ntemperature: 0.5\ndisable_thinking: true\ninject: minimal\n---\n\n# 身份\n你好"
	meta, body := parseRoleCard(raw)
	if meta["temperature"] != "0.5" {
		t.Fatalf("temperature = %q, want 0.5", meta["temperature"])
	}
	if meta["disable_thinking"] != "true" {
		t.Fatalf("disable_thinking = %q", meta["disable_thinking"])
	}
	if meta["inject"] != "minimal" {
		t.Fatalf("inject = %q", meta["inject"])
	}
	if !strings.Contains(body, "# 身份") {
		t.Fatalf("body should contain identity section, got %q", body)
	}
}

func TestCardForRole(t *testing.T) {
	// 隔离 CATA_HOME 且不 seed，走 embed 兜底，避免受本机 global/roles/ 覆盖影响。
	t.Setenv("CATA_HOME", t.TempDir())
	cases := []struct {
		role    Role
		temp    float64
		disable bool
		inject  InjectMode
	}{
		{RoleChat, 0.7, false, InjectTask},
		{RoleWorker, 0.2, true, InjectMinimal},
		{RoleEvolution, 0.2, true, InjectOff},
	}
	for _, c := range cases {
		card, err := CardForRole(c.role)
		if err != nil {
			t.Fatalf("CardForRole(%s): %v", c.role, err)
		}
		if card.Temperature != c.temp {
			t.Errorf("%s temperature = %v, want %v", c.role, card.Temperature, c.temp)
		}
		if card.DisableThinking != c.disable {
			t.Errorf("%s disable_thinking = %v, want %v", c.role, card.DisableThinking, c.disable)
		}
		if card.Inject != c.inject {
			t.Errorf("%s inject = %v, want %v", c.role, card.Inject, c.inject)
		}
		if strings.TrimSpace(card.Body) == "" {
			t.Errorf("%s body should be non-empty", c.role)
		}
	}
}

func TestRoleCardRuntimeOverride(t *testing.T) {
	t.Setenv("CATA_HOME", t.TempDir())
	if err := EnsureRoleCards(); err != nil {
		t.Fatal(err)
	}
	// 修改运行时覆盖文件，CardForRole 应立即反映（不缓存运行时）。
	p := runtimeRoleCardPath(RoleChat)
	override := "---\ntemperature: 0.9\ndisable_thinking: true\ninject: full\n---\n覆盖后的身份"
	if err := os.WriteFile(p, []byte(override), 0644); err != nil {
		t.Fatal(err)
	}
	card, err := CardForRole(RoleChat)
	if err != nil {
		t.Fatal(err)
	}
	if card.Temperature != 0.9 {
		t.Fatalf("temperature = %v, want 0.9", card.Temperature)
	}
	if !card.DisableThinking {
		t.Fatal("disable_thinking should be true")
	}
	if card.Body != "覆盖后的身份" {
		t.Fatalf("body = %q, want override", card.Body)
	}
}

func TestEnsureRoleCardsDoesNotOverwrite(t *testing.T) {
	t.Setenv("CATA_HOME", t.TempDir())
	if err := EnsureRoleCards(); err != nil {
		t.Fatal(err)
	}
	p := runtimeRoleCardPath(RoleChat)
	custom := "---\ninject: minimal\n---\n用户自定义"
	if err := os.WriteFile(p, []byte(custom), 0644); err != nil {
		t.Fatal(err)
	}
	// 再次 EnsureRoleCards 不应覆盖用户自定义。
	if err := EnsureRoleCards(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != custom {
		t.Fatalf("EnsureRoleCards overwrote user edit: %q", string(data))
	}
}

func TestEnsureRoleCardsRefreshesOldSeed(t *testing.T) {
	t.Setenv("CATA_HOME", t.TempDir())
	if err := EnsureRoleCards(); err != nil {
		t.Fatal(err)
	}
	p := runtimeRoleCardPath(RoleChat)
	// 模拟旧 seed（seed_version 0），应被内置模板覆盖。
	old := "---\nseed_version: 0\ntemperature: 0.7\ninject: task\n---\n旧版身份"
	if err := os.WriteFile(p, []byte(old), 0644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureRoleCards(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), "seed_version: 1") {
		t.Fatalf("old seed should be refreshed to seed_version 1: %q", string(data))
	}
}

func TestInjectProfile(t *testing.T) {
	cases := []struct {
		in   InjectMode
		want brain.PromptProfile
	}{
		{InjectMinimal, brain.PromptProfileMinimal},
		{InjectTask, brain.PromptProfileTask},
		{InjectFull, brain.PromptProfileFull},
		{InjectOff, brain.PromptProfileFull}, // off 无档位，返回 full 仅作占位
	}
	for _, c := range cases {
		if got := (RoleCard{Inject: c.in}).InjectProfile(); got != c.want {
			t.Errorf("InjectProfile(%s) = %v, want %v", c.in, got, c.want)
		}
	}
}
