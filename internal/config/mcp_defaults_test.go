package config

import "testing"

func TestMigrateLegacyPlaywrightMCPArgs(t *testing.T) {
	tests := []struct {
		in   []string
		want []string
		ok   bool
	}{
		{[]string{"-y", "@playwright/mcp@latest", "--console"}, defaultPlaywrightMCPArgs(), true},
		{[]string{"-y", "@playwright/mcp@latest"}, defaultPlaywrightMCPArgs(), true},
		{[]string{"-y", "@playwright/mcp@0.0.75", "--extension"}, []string{"-y", "@playwright/mcp@0.0.75", "--extension"}, false},
		{[]string{"-y", "@playwright/mcp@latest", "--extension"}, []string{"-y", "@playwright/mcp@latest", "--extension"}, false},
	}
	for _, tt := range tests {
		got, ok := migrateLegacyPlaywrightMCPArgs(tt.in)
		if ok != tt.ok {
			t.Fatalf("migrate(%v): ok=%v want %v", tt.in, ok, tt.ok)
		}
		if len(got) != len(tt.want) {
			t.Fatalf("migrate(%v): got %v want %v", tt.in, got, tt.want)
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Fatalf("migrate(%v): got %v want %v", tt.in, got, tt.want)
			}
		}
	}
}

func TestNormalizeMCPConfigDefaultTimeout(t *testing.T) {
	m := &MCPConfig{Enabled: true}
	normalizeMCPConfig(m)
	if m.ToolTimeoutSeconds != 300 {
		t.Fatalf("timeout=%d want 300", m.ToolTimeoutSeconds)
	}
}
