package server

import (
	"errors"
	"testing"
)

func TestToolResultLevelByToolName(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"read_file", displaySilent},
		{"read_skill", displaySilent},
		{"list_files", displaySilent},
		{"list_modes", displaySilent},
		{"search_replace", displayNormal},
		{"append_file", displayNormal},
		{"create_file", displayNormal},
		{"run_skill", displayNormal},
		{"run_command", displayVerbose},
		{"unknown_tool", displayVerbose},
	}
	for _, c := range cases {
		if got := toolResultLevel(c.name, "ok", nil); got != c.want {
			t.Errorf("toolResultLevel(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestToolResultLevelErrorForcesVerbose(t *testing.T) {
	// 即使 read_file 这类 silent 工具，出错也必须 verbose，让用户看到错误。
	if got := toolResultLevel("read_file", "", errors.New("boom")); got != displayVerbose {
		t.Errorf("error should force verbose, got %q", got)
	}
	if got := toolResultLevel("search_replace", "[error] not found", nil); got != displayVerbose {
		t.Errorf("error-ish output should force verbose, got %q", got)
	}
}

func TestToolDisplayLevelForStart(t *testing.T) {
	if got := toolDisplayLevelForStart("read_file"); got != displaySilent {
		t.Errorf("start read_file should be silent, got %q", got)
	}
	if got := toolDisplayLevelForStart("run_command"); got != displayNormal {
		t.Errorf("start run_command should be normal, got %q", got)
	}
}

func TestIsToolErrorOutput(t *testing.T) {
	ok := []string{"ok", "wrote 12 bytes", "no output"}
	for _, s := range ok {
		if isToolErrorOutput(s) {
			t.Errorf("isToolErrorOutput(%q) = true, want false", s)
		}
	}
	bad := []string{"[error] boom", "Error: missing", "exit status 1"}
	for _, s := range bad {
		if !isToolErrorOutput(s) {
			t.Errorf("isToolErrorOutput(%q) = false, want true", s)
		}
	}
}
