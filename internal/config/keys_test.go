package config

import "testing"

func TestGetSetMCPKeys(t *testing.T) {
	cfg := getDefaultConfig()

	if _, ok, err := GetKey(cfg, "mcp.tool_timeout_seconds"); err != nil || !ok {
		t.Fatalf("get mcp.tool_timeout_seconds: ok=%v err=%v", ok, err)
	}

	if _, err := SetKey(cfg, "mcp.tool_timeout_seconds", "450"); err != nil {
		t.Fatalf("set timeout: %v", err)
	}
	if cfg.MCP.ToolTimeoutSeconds != 450 {
		t.Fatalf("timeout=%d want 450", cfg.MCP.ToolTimeoutSeconds)
	}

	argsJSON := `["-y","@playwright/mcp@0.0.75","--extension"]`
	if _, err := SetKey(cfg, "mcp.browser.args", argsJSON); err != nil {
		t.Fatalf("set browser args: %v", err)
	}
	v, ok, err := GetKey(cfg, "mcp.browser.args")
	if err != nil || !ok {
		t.Fatalf("get browser args: ok=%v err=%v", ok, err)
	}
	if v != argsJSON {
		t.Fatalf("browser args=%q want %q", v, argsJSON)
	}

	if _, err := SetKey(cfg, "mcp.allowed_tools", "browser_*,run_skill"); err != nil {
		t.Fatalf("set allowed_tools: %v", err)
	}
	if len(cfg.MCP.AllowedTools) != 2 || cfg.MCP.AllowedTools[0] != "browser_*" {
		t.Fatalf("allowed_tools=%v", cfg.MCP.AllowedTools)
	}
}

func TestSetKeyUnknown(t *testing.T) {
	cfg := getDefaultConfig()
	if _, err := SetKey(cfg, "nope.key", "x"); err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestConfigKeysSorted(t *testing.T) {
	keys := ConfigKeys()
	if len(keys) == 0 {
		t.Fatal("expected keys")
	}
	for i := 1; i < len(keys); i++ {
		if keys[i] < keys[i-1] {
			t.Fatalf("keys not sorted: %q before %q", keys[i-1], keys[i])
		}
	}
}

func TestRedactConfig(t *testing.T) {
	cfg := getDefaultConfig()
	cfg.LLM.APIKey = "secret"
	out := RedactConfig(cfg)
	if out.LLM.APIKey != "***hidden***" {
		t.Fatalf("api key not redacted")
	}
	if cfg.LLM.APIKey != "secret" {
		t.Fatal("redact should copy, not mutate original")
	}
}
