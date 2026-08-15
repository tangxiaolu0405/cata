package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
	"cata/internal/cata/protocol"
)

// setupManageMCPTest 写临时 config + workspace，返回清理函数。
func setupManageMCPTest(t *testing.T) (*brain.Workspace, func()) {
	t.Helper()
	cfgJSON := `{"mcp":{"enabled":false,"servers":[
		{"name":"browser","enabled":true,"command":"npx","args":["-y","@playwright/mcp@0.0.75"]},
		{"name":"codegraph","enabled":true,"command":"codegraph","args":["serve"]}
	]}}`
	return setupManageMCPTestWithConfig(t, cfgJSON)
}

// setupManageMCPTestWithConfig 用指定 config JSON 写临时 config + workspace。
// 测试里 MCP server 命令请用不存在的二进制（如 /nonexistent-cata-test-bin），
// 避免 Reload 阶段真实拉起 npx / node 进程。
func setupManageMCPTestWithConfig(t *testing.T, cfgJSON string) (*brain.Workspace, func()) {
	t.Helper()
	home := t.TempDir()
	cfgPath := filepath.Join(home, "config.json")
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvConfigFile, cfgPath)

	oldCfg := config.Config
	restore := func() { config.Config = oldCfg }

	if _, err := config.LoadConfig(); err != nil {
		restore()
		t.Fatal(err)
	}

	root := t.TempDir()
	ws := &brain.Workspace{ID: "ws", RootPath: root, ActiveMode: brain.ModeDefaultID}
	if err := os.MkdirAll(ws.ModeDir(brain.ModeDefaultID), 0755); err != nil {
		restore()
		t.Fatal(err)
	}
	if err := os.WriteFile(ws.CapabilitiesPath(), []byte("skills: []\nmcp: []\n"), 0644); err != nil {
		restore()
		t.Fatal(err)
	}
	return ws, restore
}

func capabilitiesContains(t *testing.T, ws *brain.Workspace, sub string) bool {
	t.Helper()
	data, err := os.ReadFile(ws.CapabilitiesPath())
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(data), sub)
}

// countGlobalServerDefs 读取全局 config.json，统计 name==server 的定义条数（区分大小写不敏感）。
func countGlobalServerDefs(t *testing.T, server string) int {
	t.Helper()
	var c struct {
		MCP struct {
			Servers []struct {
				Name string `json:"name"`
			} `json:"servers"`
		} `json:"mcp"`
	}
	data := mustRead(t, config.GetConfigPath())
	if err := json.Unmarshal([]byte(data), &c); err != nil {
		t.Fatalf("parse global config: %v\n%s", err, data)
	}
	n := 0
	for _, s := range c.MCP.Servers {
		if strings.EqualFold(strings.TrimSpace(s.Name), server) {
			n++
		}
	}
	return n
}

func TestManageMCPEnableWritesProjectCapabilities(t *testing.T) {
	ws, restore := setupManageMCPTest(t)
	defer restore()

	ctx := withChatWorkspace(context.Background(), ws)
	tool := &manageMCPTool{ss: &SocketServer{}}
	if _, err := tool.Execute(ctx, nil, `{"action":"enable","server":"codegraph"}`); err != nil {
		t.Fatal(err)
	}
	if !capabilitiesContains(t, ws, "codegraph") {
		t.Fatalf("capabilities should contain codegraph:\n%s", mustRead(t, ws.CapabilitiesPath()))
	}
	// 幂等：再次 enable 不报错、不重复。
	if _, err := tool.Execute(ctx, nil, `{"action":"enable","server":"codegraph"}`); err != nil {
		t.Fatal(err)
	}
	if c := strings.Count(mustRead(t, ws.CapabilitiesPath()), "codegraph"); c != 1 {
		t.Fatalf("codegraph appears %d times, want 1", c)
	}
}

func TestManageMCPInstallExistingOnlyEnablesProject(t *testing.T) {
	// 验收 #3：全局已有同名定义 → install 只启用项目，不改全局、不重复写。
	ws, restore := setupManageMCPTest(t)
	defer restore()

	ctx := withChatWorkspace(context.Background(), ws)
	tool := &manageMCPTool{ss: &SocketServer{}}
	if _, err := tool.Execute(ctx, nil, `{"action":"install","server":"codegraph","command":"other","args":["x"]}`); err != nil {
		t.Fatal(err)
	}
	if n := countGlobalServerDefs(t, "codegraph"); n != 1 {
		t.Fatalf("global config should keep single codegraph definition (got %d):\n%s", n, mustRead(t, config.GetConfigPath()))
	}
	if strings.Contains(mustRead(t, config.GetConfigPath()), "other") {
		t.Fatalf("global config should NOT be rewritten with new command:\n%s", mustRead(t, config.GetConfigPath()))
	}
	if !capabilitiesContains(t, ws, "codegraph") {
		t.Fatalf("project should be enabled:\n%s", mustRead(t, ws.CapabilitiesPath()))
	}
}

func TestManageMCPDisableRemovesProjectEntry(t *testing.T) {
	ws, restore := setupManageMCPTest(t)
	defer restore()

	ctx := withChatWorkspace(context.Background(), ws)
	tool := &manageMCPTool{ss: &SocketServer{}}
	if _, err := tool.Execute(ctx, nil, `{"action":"enable","server":"browser"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(ctx, nil, `{"action":"disable","server":"browser"}`); err != nil {
		t.Fatal(err)
	}
	if capabilitiesContains(t, ws, "browser") {
		t.Fatalf("browser should be removed from capabilities:\n%s", mustRead(t, ws.CapabilitiesPath()))
	}
	// disable 后显式空 mcp 节 = 禁用全部，不能再被默认 browser 顶回。
	if caps := brain.LoadCapabilitiesFor(ws); caps.AllowsMCPServer("browser") {
		t.Fatalf("browser should NOT be re-enabled by default after disable: %+v", caps.MCP)
	}
	// 全局定义仍在（disable 只动项目）。
	data := mustRead(t, config.GetConfigPath())
	if !strings.Contains(data, "browser") {
		t.Fatalf("global definition should remain:\n%s", data)
	}
}

func TestManageMCPList(t *testing.T) {
	ws, restore := setupManageMCPTest(t)
	defer restore()

	ctx := withChatWorkspace(context.Background(), ws)
	tool := &manageMCPTool{ss: &SocketServer{}}
	out, err := tool.Execute(ctx, nil, `{"action":"list"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "browser") || !strings.Contains(out, "codegraph") {
		t.Fatalf("list should include global definitions:\n%s", out)
	}
	if _, err := tool.Execute(ctx, nil, `{"action":"enable","server":"codegraph"}`); err != nil {
		t.Fatal(err)
	}
	out, err = tool.Execute(ctx, nil, `{"action":"list"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "current project enabled") || !strings.Contains(out, "codegraph") {
		t.Fatalf("list should include project enabled:\n%s", out)
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// confirmClient 模拟客户端：读取 exec_confirm_required 事件并按 approved 回复。
func confirmClient(t *testing.T, client net.Conn, approved bool) {
	t.Helper()
	dec := json.NewDecoder(client)
	for {
		var ev map[string]any
		if err := dec.Decode(&ev); err != nil {
			t.Errorf("confirm client decode: %v", err)
			return
		}
		if ev["type"] == "exec_confirm_required" {
			id, _ := ev["confirm_id"].(string)
			if _, err := fmt.Fprintf(client, `{"command":"exec_confirm","confirm_id":%q,"approved":%v}`+"\n", id, approved); err != nil {
				t.Errorf("confirm client write: %v", err)
			}
			return
		}
	}
}

func TestManageMCPInstallNewServerWritesGlobalAndEnables(t *testing.T) {
	// 验收 #1：install 新 server → 用户确认 → 全局定义 + 项目启用 + 免重启（Reload）。
	cfgJSON := `{"mcp":{"enabled":true,"servers":[
		{"name":"browser","enabled":true,"command":"/nonexistent-cata-test-bin","args":["x"]}
	]}}`
	ws, restore := setupManageMCPTestWithConfig(t, cfgJSON)
	defer restore()

	client, conn := net.Pipe()
	br := bufio.NewReader(conn)
	lr := protocol.NewConnLineReader(br, conn, nil)
	defer lr.Stop()

	ctx := withChatWorkspace(context.Background(), ws)
	ctx = protocol.WithChatConnReader(ctx, lr)
	go confirmClient(t, client, true)

	tool := &manageMCPTool{ss: &SocketServer{}}
	out, err := tool.Execute(ctx, conn, `{"action":"install","server":"codegraph","command":"/nonexistent-cata-test-bin","args":["serve"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "codegraph") {
		t.Fatalf("install output should mention codegraph: %s", out)
	}
	if n := countGlobalServerDefs(t, "codegraph"); n != 1 {
		t.Fatalf("global should have exactly one codegraph definition (got %d):\n%s", n, mustRead(t, config.GetConfigPath()))
	}
	if !capabilitiesContains(t, ws, "codegraph") {
		t.Fatalf("project should enable codegraph:\n%s", mustRead(t, ws.CapabilitiesPath()))
	}
}

func TestManageMCPInstallNewServerCancelledWritesNothing(t *testing.T) {
	cfgJSON := `{"mcp":{"enabled":true,"servers":[
		{"name":"browser","enabled":true,"command":"/nonexistent-cata-test-bin","args":["x"]}
	]}}`
	ws, restore := setupManageMCPTestWithConfig(t, cfgJSON)
	defer restore()

	client, conn := net.Pipe()
	br := bufio.NewReader(conn)
	lr := protocol.NewConnLineReader(br, conn, nil)
	defer lr.Stop()

	ctx := withChatWorkspace(context.Background(), ws)
	ctx = protocol.WithChatConnReader(ctx, lr)
	go confirmClient(t, client, false)

	tool := &manageMCPTool{ss: &SocketServer{}}
	out, err := tool.Execute(ctx, conn, `{"action":"install","server":"codegraph","command":"/nonexistent-cata-test-bin","args":["serve"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "cancelled") {
		t.Fatalf("cancelled output expected: %s", out)
	}
	data := mustRead(t, config.GetConfigPath())
	if strings.Contains(data, "codegraph") {
		t.Fatalf("cancelled install should NOT write global:\n%s", data)
	}
	if capabilitiesContains(t, ws, "codegraph") {
		t.Fatalf("cancelled install should NOT enable project:\n%s", mustRead(t, ws.CapabilitiesPath()))
	}
}
