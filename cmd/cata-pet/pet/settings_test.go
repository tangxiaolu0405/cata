package pet

import "testing"

func TestDefaultSettings(t *testing.T) {
	s := defaultSettings()
	if !s.AlwaysOnTop {
		t.Fatal("always on top should default true")
	}
	if s.Cwd == "" {
		t.Fatal("cwd should be set")
	}
}

func TestFindCataBinaryEmptyEnv(t *testing.T) {
	// May or may not find cata; just ensure it does not panic.
	_, _ = FindCataBinary()
}
