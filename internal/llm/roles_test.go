package llm

import (
	"testing"

	"cata/internal/config"
)

func TestResolveModelForRole_fallbackSingleModel(t *testing.T) {
	cfg := config.AppConfig{
		LLM: config.LLMConfig{
			Model: "only-one-model",
		},
	}
	got := resolveModelForRole(cfg.LLM, RoleChat)
	if got != "only-one-model" {
		t.Fatalf("chat=%q want only-one-model", got)
	}
	got = resolveModelForRole(cfg.LLM, RoleWorker)
	if got != "only-one-model" {
		t.Fatalf("worker=%q want only-one-model", got)
	}
}

func TestResolveModelForRole_perRoleOverride(t *testing.T) {
	cfg := config.AppConfig{
		LLM: config.LLMConfig{
			Model: "base-model",
			Models: map[string]string{
				"chat":      "strong-model",
				"evolution": "fast-model",
			},
		},
	}
	if got := resolveModelForRole(cfg.LLM, RoleChat); got != "strong-model" {
		t.Fatalf("chat=%q", got)
	}
	if got := resolveModelForRole(cfg.LLM, RoleEvolution); got != "fast-model" {
		t.Fatalf("evolution=%q", got)
	}
	if got := resolveModelForRole(cfg.LLM, RoleWorker); got != "base-model" {
		t.Fatalf("worker should fall back to base, got %q", got)
	}
}

func TestResolveModelForRole_defaultInMap(t *testing.T) {
	cfg := config.AppConfig{
		LLM: config.LLMConfig{
			Model: "ignored-if-default-set",
			Models: map[string]string{
				"default": "shared-default",
				"worker":  "worker-only",
			},
		},
	}
	if got := resolveModelForRole(cfg.LLM, RoleChat); got != "shared-default" {
		t.Fatalf("chat via default=%q", got)
	}
	if got := resolveModelForRole(cfg.LLM, RoleWorker); got != "worker-only" {
		t.Fatalf("worker=%q", got)
	}
}
