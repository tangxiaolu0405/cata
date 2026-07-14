package client

import (
	"testing"
)

func TestSanitizeSocketLineStripsNulls(t *testing.T) {
	got := string(sanitizeSocketLine([]byte{0, 0, '{', '"', 't', '"', ':', '1', '}', 0}))
	want := `{"t":1}`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
