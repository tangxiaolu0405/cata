package brain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCapabilities(t *testing.T, w *Workspace, content string) {
	t.Helper()
	dir := w.ModeDir(w.modeID())
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileCapabilities), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func readCapabilitiesFile(t *testing.T, w *Workspace) string {
	t.Helper()
	data, err := os.ReadFile(w.CapabilitiesPath())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAppendMCPToCapabilities(t *testing.T) {
	root := t.TempDir()
	w := &Workspace{ID: "ws", RootPath: root, ActiveMode: ModeDefaultID}
	writeCapabilities(t, w, "skills:\n  - demo\nmcp:\n  - browser\n")

	if err := AppendMCPToCapabilities(w, "codegraph"); err != nil {
		t.Fatal(err)
	}
	caps := LoadCapabilitiesFor(w)
	if !caps.AllowsMCPServer("codegraph") {
		t.Fatalf("codegraph should be enabled: %+v", caps)
	}
	if !caps.AllowsMCPServer("browser") {
		t.Fatalf("browser should remain: %+v", caps)
	}
	if len(caps.Skills) != 1 || caps.Skills[0] != "demo" {
		t.Fatalf("skills should be preserved: %+v", caps.Skills)
	}

	// 幂等：重复 append 不产生重复项。
	if err := AppendMCPToCapabilities(w, "codegraph"); err != nil {
		t.Fatal(err)
	}
	caps = LoadCapabilitiesFor(w)
	n := 0
	for _, m := range caps.MCP {
		if m == "codegraph" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("duplicate mcp entries: %+v", caps.MCP)
	}
}

func TestAppendMCPToCapabilitiesEmptyFile(t *testing.T) {
	root := t.TempDir()
	w := &Workspace{ID: "ws", RootPath: root, ActiveMode: ModeDefaultID}
	writeCapabilities(t, w, "skills: []\nmcp: []\n")
	if err := AppendMCPToCapabilities(w, "codegraph"); err != nil {
		t.Fatal(err)
	}
	caps := LoadCapabilitiesFor(w)
	if !caps.AllowsMCPServer("codegraph") {
		t.Fatalf("codegraph should be enabled: %+v", caps)
	}
}

func TestRemoveMCPFromCapabilities(t *testing.T) {
	root := t.TempDir()
	w := &Workspace{ID: "ws", RootPath: root, ActiveMode: ModeDefaultID}
	writeCapabilities(t, w, "skills:\n  - demo\nmcp:\n  - browser\n  - codegraph\n")

	if err := RemoveMCPFromCapabilities(w, "codegraph"); err != nil {
		t.Fatal(err)
	}
	out := readCapabilitiesFile(t, w)
	if containsLine(out, "- codegraph") {
		t.Fatalf("codegraph should be removed:\n%s", out)
	}
	if !containsLine(out, "- browser") {
		t.Fatalf("browser should remain:\n%s", out)
	}
	if !containsLine(out, "- demo") {
		t.Fatalf("skills should remain:\n%s", out)
	}

	// 移除不存在的名称为 no-op。
	if err := RemoveMCPFromCapabilities(w, "codegraph"); err != nil {
		t.Fatal(err)
	}
}

func containsLine(s, sub string) bool {
	for _, line := range splitLines(s) {
		if strings.TrimSpace(line) == strings.TrimSpace(sub) {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, cur)
	return out
}

func TestAppendMCPToCapabilitiesNilWorkspace(t *testing.T) {
	if err := AppendMCPToCapabilities(nil, "foo"); err == nil {
		t.Fatal("nil workspace should error")
	}
	if err := AppendMCPToCapabilities(&Workspace{}, ""); err == nil {
		t.Fatal("empty name should error")
	}
}

func TestParseCapabilitiesExplicitEmptyMCPDisablesAll(t *testing.T) {
	// 显式 `mcp:` 空节 = 禁用全部 MCP（manage_mcp disable 的落点），不能被默认 browser 顶掉。
	root := t.TempDir()
	w := &Workspace{ID: "ws", RootPath: root, ActiveMode: ModeDefaultID}
	writeCapabilities(t, w, "skills: []\nmcp: []\n")

	caps := LoadCapabilitiesFor(w)
	if len(caps.MCP) != 0 {
		t.Fatalf("explicit empty mcp should disable all, got %+v", caps.MCP)
	}
	if caps.AllowsMCPServer("browser") {
		t.Fatal("browser should NOT be allowed after explicit empty mcp")
	}
	if caps.AllowsMCPServer("codegraph") {
		t.Fatal("codegraph should NOT be allowed after explicit empty mcp")
	}
}

func TestParseCapabilitiesNoMCPSectionDefaultsBrowser(t *testing.T) {
	// 没有 mcp 节（老项目/缺失文件）→ 兼容默认 browser。
	root := t.TempDir()
	w := &Workspace{ID: "ws", RootPath: root, ActiveMode: ModeDefaultID}
	writeCapabilities(t, w, "skills:\n  - demo\n")

	caps := LoadCapabilitiesFor(w)
	if !caps.AllowsMCPServer("browser") {
		t.Fatalf("missing mcp section should default to browser: %+v", caps.MCP)
	}
}
