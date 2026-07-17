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
		t.Fatal("expected heredoc/multiline hint")
	}
}

func TestCheckRunCommandArgv_multilineRejected(t *testing.T) {
	argv := []string{"wsl.exe", "-e", "bash", "-lc", "cd /mnt/d/stock && python3 -c \"\nimport csv\nprint(1)\n\""}
	if err := checkRunCommandArgv(argv); err == nil {
		t.Fatal("expected multiline rejection")
	}
}

func TestCheckRunCommandArgv_inlineCodeRejected(t *testing.T) {
	script := `cd /mnt/d/stock && python3 -c "import csv, io; open('a'); open('b'); print('x'*50)"`
	argv := []string{"wsl.exe", "-e", "bash", "-lc", script}
	if err := checkRunCommandArgv(argv); err == nil {
		t.Fatal("expected inline -c rejection")
	}
	node := `node -e "const fs=require('fs'); fs.writeFileSync('a.txt','x'.repeat(80)); console.log(1)"`
	if err := checkRunCommandArgv([]string{"wsl.exe", "-e", "bash", "-lc", node}); err == nil {
		t.Fatal("expected node -e rejection")
	}
}

func TestCheckRunCommandArgv_shortOk(t *testing.T) {
	argv := []string{"wsl.exe", "-e", "bash", "-lc", "echo ok"}
	if err := checkRunCommandArgv(argv); err != nil {
		t.Fatal(err)
	}
}

func TestCheckRunCommandArgv_shortScriptFileOk(t *testing.T) {
	argv := []string{"wsl.exe", "-e", "bash", "-lc", "python3 clean_zt.py"}
	if err := checkRunCommandArgv(argv); err != nil {
		t.Fatal(err)
	}
}
