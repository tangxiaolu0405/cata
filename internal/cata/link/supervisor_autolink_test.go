package link

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cata/internal/cata/brain"
)

// makeTestWorkspace 在 home 脑格下造一个带 kind 的工作空间格（meta.json + 真实目录）。
func makeTestWorkspace(t *testing.T, home, id, rootPath, kind string) {
	t.Helper()
	cell := filepath.Join(home, "brain", "workspaces", id)
	if err := os.MkdirAll(cell, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rootPath, 0755); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(map[string]string{
		"id":        id,
		"root_path": rootPath,
		"kind":      kind,
		"name":      id,
	})
	if err := os.WriteFile(filepath.Join(cell, "meta.json"), meta, 0644); err != nil {
		t.Fatal(err)
	}
}

// TestAutoLinkExistingWorkspacesKindFilter 验证自动接入只接 git/marked，跳过 ephemeral。
func TestAutoLinkExistingWorkspacesKindFilter(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	if err := brain.EnsureCataLayout(); err != nil {
		t.Fatal(err)
	}

	// git 项目 → 应接入
	gitProj := filepath.Join(home, "proj-git")
	makeTestWorkspace(t, home, "ws-git", gitProj, string(brain.KindGit))
	// marked 项目 → 应接入
	markedProj := filepath.Join(home, "proj-marked")
	makeTestWorkspace(t, home, "ws-marked", markedProj, string(brain.KindMarked))
	// ephemeral 临时工作区 → 不应接入
	ephProj := filepath.Join(home, "tmp-ephemeral")
	makeTestWorkspace(t, home, "ws-eph", ephProj, string(brain.KindEphemeral))

	cfg, _ := LoadConfig()
	if err := autoLinkExistingWorkspaces(cfg); err != nil {
		t.Fatal(err)
	}

	got, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	// Add 内部用 ResolveWorkspace 重新生成 agent_id（路径派生），故按 root_path 断言。
	gitID := agentIDForRoot(t, got, gitProj)
	if gitID == "" {
		t.Error("git workspace should be auto-linked")
	}
	markedID := agentIDForRoot(t, got, markedProj)
	if markedID == "" {
		t.Error("marked workspace should be auto-linked")
	}
	if id := agentIDForRoot(t, got, ephProj); id != "" {
		t.Errorf("ephemeral workspace should NOT be auto-linked (got agent %s)", id)
	}
	if gitID != "" {
		if e := got.Agents[gitID]; !e.KeepAlive || !e.Enabled {
			t.Errorf("auto-linked agent should be keep-alive+enabled: %+v", e)
		}
	}
}

// agentIDForRoot 在 cfg.Agents 中按 root_path 反查 agent_id（空 = 未注册）。
func agentIDForRoot(t *testing.T, cfg Config, root string) string {
	t.Helper()
	root = filepath.Clean(root)
	for id, e := range cfg.Agents {
		if filepath.Clean(e.RootPath) == root {
			return id
		}
	}
	return ""
}

// TestAutoLinkExistingWorkspacesIdempotent 已注册的 agent 不重复 Add、不刷新 linked_at。
func TestAutoLinkExistingWorkspacesIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	if err := brain.EnsureCataLayout(); err != nil {
		t.Fatal(err)
	}

	proj := filepath.Join(home, "proj-git")
	makeTestWorkspace(t, home, "ws-git", proj, string(brain.KindGit))

	// 预先注册（模拟 cata link add 已手动接入）。
	pre := Config{
		Agents: map[string]AgentEntry{
			"ws-git": {AgentID: "ws-git", RootPath: proj, Name: "ws-git",
				KeepAlive: true, Enabled: true, LinkedAt: "2026-01-01T00:00:00Z"},
		},
	}
	if err := SaveConfig(pre); err != nil {
		t.Fatal(err)
	}

	cfg, _ := LoadConfig()
	if err := autoLinkExistingWorkspaces(cfg); err != nil {
		t.Fatal(err)
	}

	got, _ := LoadConfig()
	if got.Agents["ws-git"].LinkedAt != "2026-01-01T00:00:00Z" {
		t.Errorf("auto-link should be idempotent (not refresh linked_at): %+v", got.Agents["ws-git"])
	}
}

// TestAutoLinkExistingWorkspacesSkipsHomeRoot home 目录/CATA_HOME 作为 root_path 的格子不接入。
func TestAutoLinkExistingWorkspacesSkipsHomeRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	if err := brain.EnsureCataLayout(); err != nil {
		t.Fatal(err)
	}

	// 造一个 root_path = 用户 home 的 git 格子（如 users-lucas 整家目录格）。
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	makeTestWorkspace(t, home, "ws-home", userHome, string(brain.KindGit))

	cfg, _ := LoadConfig()
	if err := autoLinkExistingWorkspaces(cfg); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadConfig()
	if got.HasAgent("ws-home") {
		t.Error("home-dir workspace should NOT be auto-linked (sensitive)")
	}
}

// TestAutoLinkExistingWorkspacesEmpty 无工作空间 / 无 link.json 时无副作用。
func TestAutoLinkExistingWorkspacesEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	if err := brain.EnsureCataLayout(); err != nil {
		t.Fatal(err)
	}
	cfg, _ := LoadConfig()
	if err := autoLinkExistingWorkspaces(cfg); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadConfig()
	if len(got.Agents) != 0 {
		t.Fatalf("no workspaces should be linked, got %d", len(got.Agents))
	}
}
