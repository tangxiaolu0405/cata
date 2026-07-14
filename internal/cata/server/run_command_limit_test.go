package server

import (
	"runtime"
	"strings"
	"testing"
)

func TestCheckRunCommandArgv_windowsLongLine(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only")
	}
	argv := []string{"wsl.exe", "-e", "bash", "-lc", strings.Repeat("x", 9000)}
	if err := checkRunCommandArgv(argv); err == nil {
		t.Fatal("expected length error")
	}
}

func TestCheckRunCommandArgv_heredocHint(t *testing.T) {
	body := strings.Repeat("line\n", 300)
	argv := []string{"wsl.exe", "-e", "bash", "-lc", "cat > /tmp/a << 'EOF'\n" + body + "EOF"}
	if err := checkRunCommandArgv(argv); err == nil {
		t.Fatal("expected heredoc hint")
	}
}

func TestCheckRunCommandArgv_shortOk(t *testing.T) {
	argv := []string{"wsl.exe", "-e", "bash", "-lc", "echo ok"}
	if err := checkRunCommandArgv(argv); err != nil {
		t.Fatal(err)
	}
}
