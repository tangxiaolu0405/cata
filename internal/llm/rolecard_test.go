package llm

import (
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
