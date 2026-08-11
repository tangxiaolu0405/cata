package config

import (
	"testing"
)

func TestNormalizeSchedulesConfigDefaults(t *testing.T) {
	s := &SchedulesConfig{}
	normalizeSchedulesConfig(s)
	if s.Enabled == nil || !*s.Enabled {
		t.Fatalf("Enabled should default true, got %v", s.Enabled)
	}
	if s.TickSeconds != 30 {
		t.Fatalf("TickSeconds=%d want 30", s.TickSeconds)
	}
}

func TestNormalizeSchedulesConfigPreservesExplicitFalse(t *testing.T) {
	f := false
	s := &SchedulesConfig{Enabled: &f}
	normalizeSchedulesConfig(s)
	if s.Enabled == nil || *s.Enabled {
		t.Fatalf("explicit false should be preserved, got %v", *s.Enabled)
	}
}

func TestSchedulesHelpersDefaultWhenConfigNil(t *testing.T) {
	old := Config
	defer func() { Config = old }()
	Config = nil
	if !SchedulesEnabled() {
		t.Fatal("SchedulesEnabled should default true when Config nil")
	}
}

func TestSchedulesHelpersRespectConfigValue(t *testing.T) {
	old := Config
	defer func() { Config = old }()
	f := false
	Config = &AppConfig{Schedules: SchedulesConfig{Enabled: &f, TickSeconds: 15}}
	if SchedulesEnabled() {
		t.Fatal("SchedulesEnabled should be false")
	}
	if SchedulesTick() != 15_000_000_000 { // 15s
		t.Fatalf("SchedulesTick=%v want 15s", SchedulesTick())
	}
}

func TestAppConfigKnownTopKeysIncludesSchedules(t *testing.T) {
	for _, k := range AppConfigKnownTopKeys() {
		if k == "schedules" {
			return
		}
	}
	t.Fatal("schedules should be a known top-level config key")
}
