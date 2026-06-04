package brain

import (
	"strings"
	"testing"
)

func TestShellSyntaxLabel(t *testing.T) {
	e := RuntimeEnv{OS: "windows", Shell: "powershell"}
	if e.ShellSyntaxLabel() != "PowerShell" {
		t.Fatalf("got %q", e.ShellSyntaxLabel())
	}
}

func TestSidebarToolLines(t *testing.T) {
	tools := HostTools{Node: "/usr/bin/node"}
	lines := tools.SidebarToolLines()
	if len(lines) < 2 || !strings.Contains(lines[1], "node  ✓") {
		t.Fatalf("lines: %v", lines)
	}
}

func TestCompactBadges(t *testing.T) {
	tools := HostTools{Python3: "/usr/bin/python3", Git: "/usr/bin/git"}
	got := tools.CompactBadges()
	if !strings.Contains(got, "py✓") || !strings.Contains(got, "nd·") {
		t.Fatalf("badges: %s", got)
	}
}

func TestCompactEnvLine(t *testing.T) {
	e := RuntimeEnv{HostOS: "windows", OS: "linux", Shell: "bash", Terminal: "wsl:ubuntu"}
	line := e.CompactEnvLine()
	if !strings.Contains(line, "windows→linux") || !strings.Contains(line, "bash-syntax") {
		t.Fatalf("line: %s", line)
	}
}

func TestToolsAvailabilityBlock(t *testing.T) {
	e := &RuntimeEnv{
		OS:       "linux",
		HostOS:   "linux",
		Shell:    "bash",
		Terminal: "wsl:ubuntu",
		Tools: HostTools{
			Python3: "/usr/bin/python3",
			Node:    "/usr/bin/node",
			Git:     "/usr/bin/git",
		},
	}
	block := e.ToolsAvailabilityBlock()
	if !strings.Contains(block, "python: yes") {
		t.Fatalf("missing python: %s", block)
	}
	if !strings.Contains(block, "node: yes") {
		t.Fatalf("missing node: %s", block)
	}
	if !strings.Contains(block, "go: **not in PATH**") {
		t.Fatalf("missing go absent: %s", block)
	}
}

func TestShellSupportsUnixSyntax(t *testing.T) {
	cases := []struct {
		env  RuntimeEnv
		want bool
	}{
		{RuntimeEnv{OS: "linux", Shell: "bash"}, true},
		{RuntimeEnv{OS: "darwin", Shell: "zsh"}, true},
		{RuntimeEnv{OS: "windows", Shell: "powershell"}, false},
		{RuntimeEnv{OS: "windows", Shell: "bash", Terminal: "git-bash:installed"}, true},
	}
	for _, c := range cases {
		if got := c.env.ShellSupportsUnixSyntax(); got != c.want {
			t.Fatalf("%+v: got %v want %v", c.env, got, c.want)
		}
	}
}

func TestHostAndCommandPlatform(t *testing.T) {
	e := RuntimeEnv{HostOS: "windows", OS: "linux", Terminal: "wsl:ubuntu"}
	if e.HostPlatform() != "windows" || e.CommandPlatform() != "linux" {
		t.Fatalf("platforms host=%s cmd=%s", e.HostPlatform(), e.CommandPlatform())
	}
}
