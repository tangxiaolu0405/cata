package brain

import (
	"strings"
	"testing"
)

func TestMCPToolNamesProvider(t *testing.T) {
	MCPToolNamesProvider = func() []string {
		return []string{"browser_navigate", "browser_snapshot"}
	}
	names := MCPToolNames()
	if len(names) != 2 || names[0] != "browser_navigate" {
		t.Fatalf("names=%v", names)
	}
	MCPToolNamesProvider = nil
}

func TestServerRegisteredToolsBlockMCPList(t *testing.T) {
	MCPToolNamesProvider = func() []string {
		return []string{"browser_click"}
	}
	defer func() { MCPToolNamesProvider = nil }()

	// 无 config 时仅验证不 panic；有 MCP provider 时由 TerminalPaths 路径覆盖。
	block := ServerRegisteredToolsBlock()
	if block == "" {
		t.Fatal("empty block")
	}
	if MCPToolNamesProvider != nil {
		n := MCPToolNames()
		if len(n) != 1 || !strings.HasPrefix(n[0], "browser_") {
			t.Fatalf("n=%v", n)
		}
	}
}
