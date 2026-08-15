package link

import (
	"os"
	"path/filepath"
	"testing"

	"cata/internal/cata/config"
)

func TestConfigSaveLoadRoundtrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)

	cfg := Config{
		GatewayURL:     "https://gw.example.com",
		Token:          "secret-token",
		DefaultAgentID: "ws-1",
		Agents: map[string]AgentEntry{
			"ws-1": {AgentID: "ws-1", RootPath: "/proj/a", Name: "Project A", KeepAlive: true, Enabled: true, LinkedAt: "2026-01-01T00:00:00Z"},
			"ws-2": {AgentID: "ws-2", RootPath: "/proj/b", Name: "Project B", KeepAlive: false, Enabled: true, LinkedAt: "2026-01-02T00:00:00Z"},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.GatewayURL != cfg.GatewayURL || got.Token != cfg.Token || got.DefaultAgentID != cfg.DefaultAgentID {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	if !got.HasAgent("ws-1") || !got.HasAgent("ws-2") {
		t.Fatal("agents not loaded")
	}
	if !got.ShouldKeepAlive("ws-1") {
		t.Fatal("ws-1 should keep-alive")
	}
	if got.ShouldKeepAlive("ws-2") {
		t.Fatal("ws-2 should not keep-alive")
	}
	if !got.TunnelEnabled("ws-1") {
		t.Fatal("ws-1 tunnel should be enabled (gateway configured + keep-alive)")
	}
	if got.TunnelEnabled("ws-2") {
		t.Fatal("ws-2 tunnel should not be enabled")
	}
	ids := got.LinkedAgentIDs()
	if len(ids) != 2 || ids[0] != "ws-1" || ids[1] != "ws-2" {
		t.Fatalf("linked ids=%v", ids)
	}
}

func TestConfigEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load empty: %v", err)
	}
	if cfg.GatewayConfigured() {
		t.Fatal("empty config should not be gateway-configured")
	}
	if cfg.HasAgent("ws-1") {
		t.Fatal("empty config should have no agents")
	}
	if cfg.TunnelEnabled("ws-1") {
		t.Fatal("empty config tunnel should be disabled")
	}
}

func TestAddRemove(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)

	dir := filepath.Join(home, "proj")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	entry, err := Add(dir, true, "https://gw.example.com", "tok")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if entry.AgentID == "" {
		t.Fatal("add returned empty agent id")
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HasAgent(entry.AgentID) {
		t.Fatalf("agent %q not registered", entry.AgentID)
	}
	if !cfg.GatewayConfigured() {
		t.Fatal("gateway should be configured after add with url+token")
	}

	if err := Remove(entry.AgentID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	cfg, err = LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HasAgent(entry.AgentID) {
		t.Fatal("agent still registered after remove")
	}
}

// TestResolveWorkspacePath 验证 register 子路径的越界防护：必须严格落在 workspace_root 下。
func TestResolveWorkspacePath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")

	cases := []struct {
		name    string
		subpath string
		want    string
		wantErr bool
	}{
		{"empty uses root", "", root, false},
		{"normal subdir", "foo", filepath.Join(root, "foo"), false},
		{"nested subdir", "a/b/c", filepath.Join(root, "a", "b", "c"), false},
		{"absolute rejected", "/etc", "", true},
		{"dotdot escape rejected", "../evil", "", true},
		{"dotdot nested escape rejected", "a/../../evil", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{WorkspaceRoot: root}
			got, err := ResolveWorkspacePath(cfg, tc.subpath)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("subpath=%q: expected error, got %q", tc.subpath, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("subpath=%q: %v", tc.subpath, err)
			}
			if got != tc.want {
				t.Fatalf("subpath=%q: got %q want %q", tc.subpath, got, tc.want)
			}
		})
	}
}

// TestResolveWorkspacePathRequiresRoot 无 workspace_root 时拒绝一切远程 register。
func TestResolveWorkspacePathRequiresRoot(t *testing.T) {
	if _, err := ResolveWorkspacePath(Config{}, "x"); err == nil {
		t.Fatal("expected error without workspace_root")
	}
}

// TestMachineID 验证机器标识稳定非空。
func TestMachineID(t *testing.T) {
	if MachineID() == "" {
		t.Fatal("MachineID should be non-empty")
	}
}
