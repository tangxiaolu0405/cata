package secrets

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	r := New(8)
	r.Add("sk-super-secret-key-123456")
	out := r.Redact("api key is sk-super-secret-key-123456 hello")
	if !strings.Contains(out, Placeholder) {
		t.Fatalf("expected redacted output, got %q", out)
	}
	if strings.Contains(out, "sk-super-secret-key-123456") {
		t.Fatalf("secret leaked: %q", out)
	}
}

func TestShortValuesIgnored(t *testing.T) {
	r := New(8)
	r.Add("short") // < minLen → 忽略
	if r.Count() != 0 {
		t.Fatalf("short value should be ignored, count=%d", r.Count())
	}
	r.Add("another-secret-here")
	if r.Count() != 1 {
		t.Fatalf("count=%d, want 1", r.Count())
	}
}

func TestNoSecretsNoChange(t *testing.T) {
	r := New(8)
	in := "plain text, nothing secret"
	if got := r.Redact(in); got != in {
		t.Fatalf("no-secret input changed: %q", got)
	}
}

func TestCollectFromEnvSkipsNonSecret(t *testing.T) {
	t.Setenv("SOME_NORMAL_VAR", "hello")
	t.Setenv("MY_API_KEY", "env-secret-value-1")
	t.Setenv("EMPTY_PASSWORD", "")
	vals := CollectFromEnv()
	found := false
	for _, v := range vals {
		if v == "env-secret-value-1" {
			found = true
		}
		if v == "hello" {
			t.Fatal("non-secret env name leaked")
		}
	}
	if !found {
		t.Fatal("expected MY_API_KEY value collected")
	}
}
