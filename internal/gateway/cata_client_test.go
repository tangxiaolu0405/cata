package gateway

import "testing"

func TestSplitTelegramMessage_short(t *testing.T) {
	got := SplitTelegramMessage("hello", 4096)
	if len(got) != 1 || got[0] != "hello" {
		t.Fatalf("got %v", got)
	}
}

func TestSplitTelegramMessage_empty(t *testing.T) {
	got := SplitTelegramMessage("  ", 4096)
	if len(got) != 1 || got[0] != "(empty response)" {
		t.Fatalf("got %v", got)
	}
}

func TestSplitTelegramMessage_long(t *testing.T) {
	text := ""
	for i := 0; i < 50; i++ {
		text += "line-" + string(rune('a'+i%26)) + "\n"
	}
	parts := SplitTelegramMessage(text, 100)
	if len(parts) < 2 {
		t.Fatalf("expected split, got %d parts", len(parts))
	}
	for _, p := range parts {
		if len(p) > 100 {
			t.Fatalf("part too long: %d", len(p))
		}
	}
}

func TestConfigUserAllowed(t *testing.T) {
	cfg := Config{TelegramAllowedIDs: []int64{42}}
	if !cfg.UserAllowed(42) || cfg.UserAllowed(99) {
		t.Fatal("whitelist mismatch")
	}
	open := Config{}
	if !open.UserAllowed(1) {
		t.Fatal("empty whitelist should allow all")
	}
}
