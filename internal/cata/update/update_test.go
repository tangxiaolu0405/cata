package update

import "testing"

func TestNormalizeVersion(t *testing.T) {
	cases := map[string]string{
		"v0.1.9":  "0.1.9",
		"0.1.9":   "0.1.9",
		" v1.0 ":  "1.0",
		"dev":     "dev",
		"dev-abc": "dev-abc",
		"":        "",
	}
	for in, want := range cases {
		if got := NormalizeVersion(in); got != want {
			t.Errorf("NormalizeVersion(%q)=%q want %q", in, got, want)
		}
	}
}

func TestNeedsUpdate(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v0.1.9", "v0.1.9", false},
		{"0.1.9", "v0.1.9", false},
		{"v0.1.8", "v0.1.9", true},
		{"dev", "v0.1.9", true},
		{"dev-abc1234", "v0.1.9", true},
		{"", "v0.1.9", true},
		{"v0.1.9", "", false},
	}
	for _, tc := range cases {
		if got := NeedsUpdate(tc.current, tc.latest); got != tc.want {
			t.Errorf("NeedsUpdate(%q,%q)=%v want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestDetectArtifactSmoke(t *testing.T) {
	artifact, ext, bin, gw, err := detectArtifact()
	if err != nil {
		t.Skipf("unsupported platform in CI matrix sense: %v", err)
	}
	if artifact == "" || ext == "" || bin == "" || gw == "" {
		t.Fatalf("empty artifact fields: %q %q %q %q", artifact, ext, bin, gw)
	}
}
