package link

import (
	"os"
	"path/filepath"
	"testing"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
)

func TestConfigSaveLoadRoundtrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)

	cfg := Config{
		GatewayURL:     "https://gw.example.com",
		GatewayToken:   "secret-token",
		MachineToken:   "machine-secret",
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
	if got.GatewayURL != cfg.GatewayURL || got.GatewayToken != cfg.GatewayToken || got.MachineToken != cfg.MachineToken || got.DefaultAgentID != cfg.DefaultAgentID {
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

	entry, err := Add(dir, true)
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
	// Add 不再配置网关（join 才配置），故 add 后 GatewayConfigured 应为 false。
	if cfg.GatewayConfigured() {
		t.Fatal("add alone should not configure gateway (join does)")
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
	// 预建一个 root 下的目录（绝对路径绑定测试用）。
	existingInRoot := filepath.Join(root, "existing")
	if err := os.MkdirAll(existingInRoot, 0755); err != nil {
		t.Fatal(err)
	}
	// root 之外的一个已存在目录（绝对路径绑定测试用）。
	outside := filepath.Join(t.TempDir(), "outside-proj")
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		subpath string
		want    string
		wantErr bool
	}{
		{"empty uses root", "", root, false},
		{"normal subdir", "foo", filepath.Join(root, "foo"), false},
		{"nested subdir", "a/b/c", filepath.Join(root, "a", "b", "c"), false},
		{"dotdot escape rejected", "../evil", "", true},
		{"dotdot nested escape rejected", "a/../../evil", "", true},
		// 绝对路径：已存在 → 绑定（允许在 root 之外）
		{"absolute existing in root binds", existingInRoot, existingInRoot, false},
		{"absolute existing outside root binds", outside, outside, false},
		// 绝对路径：不存在且不在 root 下 → 拒绝创建
		{"absolute missing outside root rejected", filepath.Join(t.TempDir(), "nope", "x"), "", true},
		// 绝对路径：不存在但在 root 下 → 允许创建
		{"absolute missing under root allowed", filepath.Join(root, "new-abs"), filepath.Join(root, "new-abs"), false},
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

// TestResolveWorkspacePathRequiresRoot 无 workspace_root 时：相对名拒绝、已存在绝对路径仍可绑定。
func TestResolveWorkspacePathRequiresRoot(t *testing.T) {
	if _, err := ResolveWorkspacePath(Config{}, "x"); err == nil {
		t.Fatal("expected error for relative name without workspace_root")
	}
	// 已存在的绝对路径即使无 workspace_root 也应可绑定。
	dir := t.TempDir()
	if got, err := ResolveWorkspacePath(Config{}, dir); err != nil || got != dir {
		t.Fatalf("existing abs path should bind without workspace_root: got=%q err=%v", got, err)
	}
}

// TestMachineID 验证机器标识稳定非空。
func TestMachineID(t *testing.T) {
	if MachineID() == "" {
		t.Fatal("MachineID should be non-empty")
	}
}

// TestIsHomeRootPath 验证 home 目录 / CATA_HOME 被识别为"不该自动接入"的根路径。
func TestIsHomeRootPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if !isHomeRootPath(home) {
		t.Fatalf("home %q should be home-root", home)
	}
	// 子目录不算。
	if isHomeRootPath(filepath.Join(home, "projects", "foo")) {
		t.Fatal("subdir should not be home-root")
	}
}

// TestAgentTokenPersist 验证 per-agent token 落盘/读取/幂等。
func TestAgentTokenPersist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	if err := brain.EnsureCataLayout(); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(Config{MachineID: "m1", MachineToken: "mt",
		Agents: map[string]AgentEntry{"ws-a": {AgentID: "ws-a", RootPath: "p", KeepAlive: true}}},
	); err != nil {
		t.Fatal(err)
	}

	if got := (Config{}).AgentTokenFor("ws-a"); got != "" {
		t.Fatalf("unregistered agent token=%q want empty", got)
	}
	if err := SetAgentTokenFor("ws-a", "tok-1"); err != nil {
		t.Fatal(err)
	}
	cfg, _ := LoadConfig()
	if got := cfg.AgentTokenFor("ws-a"); got != "tok-1" {
		t.Fatalf("agent token=%q want tok-1", got)
	}
	// 幂等：同值不报错。
	if err := SetAgentTokenFor("ws-a", "tok-1"); err != nil {
		t.Fatal(err)
	}
}
