package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cata/internal/cata/config"
)

func TestSaveConfigPreservesUnknownTopKeys(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.EnvCataHome, dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "gateway.json")
	initial := `{
  "edition": "base",
  "telegram_bot_token": "tok-old",
  "custom_llm_profile": {"provider": "x"},
  "notes": "keep"
}
`
	if err := os.WriteFile(path, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, extras, err := LoadGatewayDocument()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := extras["custom_llm_profile"]; !ok {
		t.Fatal("expected custom_llm_profile in extras")
	}
	cfg.TelegramBotToken = "tok-new"
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"custom_llm_profile", "notes"} {
		if _, ok := doc[key]; !ok {
			t.Fatalf("missing preserved key %q; doc=%s", key, string(raw))
		}
	}
	var tok string
	if err := json.Unmarshal(doc["telegram_bot_token"], &tok); err != nil {
		t.Fatal(err)
	}
	if tok != "tok-new" {
		t.Fatalf("token=%q", tok)
	}
}
