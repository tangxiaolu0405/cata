package brain

import (
	"testing"
	"time"

	"cata/internal/cata/clock"
)

func TestRegistryEntryRecentlyActive(t *testing.T) {
	_ = clock.Init("")
	now := clock.Now()
	cases := []struct {
		name   string
		at     string
		within time.Duration
		want   bool
	}{
		{"empty", "", 24 * time.Hour, false},
		{"invalid", "not-a-time", 24 * time.Hour, false},
		{"fresh", now.Add(-1 * time.Hour).Format(time.RFC3339), 24 * time.Hour, true},
		{"stale", now.Add(-48 * time.Hour).Format(time.RFC3339), 24 * time.Hour, false},
		{"edge", now.Add(-23 * time.Hour).Format(time.RFC3339), 24 * time.Hour, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := RegistryEntry{LastSeenAt: tc.at}
			if got := e.RecentlyActive(tc.within); got != tc.want {
				t.Fatalf("RecentlyActive(%q)=%v want %v", tc.at, got, tc.want)
			}
		})
	}
}
