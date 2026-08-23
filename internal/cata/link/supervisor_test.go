package link

import (
	"os"
	"path/filepath"
	"testing"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
)

// TestStopAllAgentsNoAgents supervisor 无注册 agent 时 stopAllAgents 应无副作用、不 panic。
func TestStopAllAgentsNoAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	if err := brain.EnsureCataLayout(); err != nil {
		t.Fatal(err)
	}
	// 清空 link.json（此时不存在 → LoadConfig 返回空）。
	s := &Supervisor{}
	s.stopAllAgents() // 不应 panic / 报错
	if _, err := os.Stat(config.LinkConfigPath()); err != nil && !os.IsNotExist(err) {
		t.Fatalf("link.json should not be created by stopAllAgents: %v", err)
	}
}

// TestStopAllAgentsStopsRegistered 有注册 agent（但 pid 文件不存在）时，stopAllAgents
// 对每个 id 尝试停止并跳过「未运行」，不中断其它项。
func TestStopAllAgentsStopsRegistered(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	if err := brain.EnsureCataLayout(); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		GatewayURL:   "http://127.0.0.1:8787",
		MachineToken: "m-tok",
		Agents: map[string]AgentEntry{
			"ws-a": {AgentID: "ws-a", RootPath: filepath.Join(home, "a"), Enabled: true, KeepAlive: true},
			"ws-b": {AgentID: "ws-b", RootPath: filepath.Join(home, "b"), Enabled: true, KeepAlive: true},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	// pid 文件不存在 → 各 agent 视为未运行，stopAllAgents 应逐个跳过并返回。
	s := &Supervisor{}
	s.stopAllAgents() // 不应 panic
}
