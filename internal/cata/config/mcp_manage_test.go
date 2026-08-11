package config

import "testing"

func TestUpsertMCPServerNew(t *testing.T) {
	c := &AppConfig{}
	added := UpsertMCPServer(c, MCPServerEntry{Name: "codegraph", Enabled: true, Command: "codegraph", Args: []string{"serve"}})
	if !added {
		t.Fatal("new server should report added")
	}
	if len(c.MCP.Servers) != 1 {
		t.Fatalf("servers=%d want 1", len(c.MCP.Servers))
	}
	if s := FindMCPServer(c, "CODEGRAPH"); s == nil || s.Command != "codegraph" {
		t.Fatalf("FindMCPServer case-insensitive failed: %+v", s)
	}
}

func TestUpsertMCPServerExistingNoDuplicate(t *testing.T) {
	c := &AppConfig{MCP: MCPConfig{Servers: []MCPServerEntry{
		{Name: "browser", Enabled: true, Command: "npx", Args: []string{"-y", "@playwright/mcp@0.0.75"}},
	}}}
	added := UpsertMCPServer(c, MCPServerEntry{Name: "Browser", Enabled: false})
	if added {
		t.Fatal("existing server should not be re-added")
	}
	if len(c.MCP.Servers) != 1 {
		t.Fatalf("servers=%d want 1 (no duplicate)", len(c.MCP.Servers))
	}
	if c.MCP.Servers[0].Enabled {
		t.Fatal("existing entry should be updated to enabled=false")
	}
	// 非空 command 才覆盖；空 command 保留原值。
	UpsertMCPServer(c, MCPServerEntry{Name: "browser", Enabled: true, Args: []string{"--extension"}})
	if c.MCP.Servers[0].Command != "npx" {
		t.Fatalf("empty command should keep original: %q", c.MCP.Servers[0].Command)
	}
	if c.MCP.Servers[0].Args[0] != "--extension" {
		t.Fatalf("args should be updated: %v", c.MCP.Servers[0].Args)
	}
}

func TestFindMCPServerNil(t *testing.T) {
	if FindMCPServer(nil, "browser") != nil {
		t.Fatal("nil config should return nil")
	}
	if FindMCPServer(&AppConfig{}, "browser") != nil {
		t.Fatal("missing server should return nil")
	}
}
