package mcp

import (
	"testing"

	"cata/internal/cata/config"
)

func TestReloadDisabledConfig(t *testing.T) {
	old := config.Config
	defer func() { config.Config = old }()

	config.Config = &config.AppConfig{}
	config.Config.MCP.Enabled = false

	Reload()
	if global == nil {
		t.Fatal("global should be non-nil (empty manager)")
	}
	if len(global.clients) != 0 || len(global.llmTools) != 0 {
		t.Fatalf("disabled config should produce empty manager: %+v", global)
	}
	if lastMCPKey != "" {
		t.Fatalf("lastMCPKey=%q want empty", lastMCPKey)
	}
}

func TestForceInitDisabledConfig(t *testing.T) {
	old := config.Config
	defer func() { config.Config = old }()

	config.Config = &config.AppConfig{}
	config.Config.MCP.Enabled = false

	ForceInit()
	if global == nil || len(global.clients) != 0 {
		t.Fatalf("ForceInit disabled should yield empty manager")
	}
}
