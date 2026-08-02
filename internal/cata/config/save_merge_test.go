package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveConfigPreservesUnknownTopKeys(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvCataHome, dir)
	t.Setenv(EnvConfigFile, "")

	path := filepath.Join(dir, DefaultAppConfigName)
	initial := `{
  "llm": {
    "provider": "deepseek",
    "api_key": "sk-old",
    "api_url": "https://api.deepseek.com/chat/completions",
    "model": "deepseek-v4-flash",
    "enabled": true
  },
  "llm_previous_qwen": {
    "provider": "qwen",
    "model": "qwen-turbo"
  },
  "llm_custom_backup": {
    "provider": "openai"
  },
  "evolution_config_help": {
    "_comment": "keep me"
  }
}
`
	if err := os.WriteFile(path, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.LLM.Provider = "mimo"
	cfg.LLM.APIKey = "sk-new"
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
	for _, key := range []string{"llm_previous_qwen", "llm_custom_backup", "evolution_config_help"} {
		if _, ok := doc[key]; !ok {
			t.Fatalf("missing preserved key %q after SaveConfig; doc=%s", key, string(raw))
		}
	}
	var llm LLMConfig
	if err := json.Unmarshal(doc["llm"], &llm); err != nil {
		t.Fatal(err)
	}
	if llm.Provider != "mimo" || llm.APIKey != "sk-new" {
		t.Fatalf("llm not updated: %+v", llm)
	}
}

func TestSaveAppConfigDocumentReplacesExtras(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvCataHome, dir)
	t.Setenv(EnvConfigFile, "")

	path := filepath.Join(dir, DefaultAppConfigName)
	if err := os.WriteFile(path, []byte(`{"llm":{"provider":"a"},"llm_old":{"x":1},"keep_me":true}`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &AppConfig{LLM: LLMConfig{Provider: "b", Enabled: true}}
	extras := map[string]json.RawMessage{
		"llm_new": json.RawMessage(`{"provider":"z"}`),
	}
	if err := SaveAppConfigDocument(cfg, extras); err != nil {
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
	if _, ok := doc["llm_old"]; ok {
		t.Fatal("llm_old should be removed when extras replaces unknowns")
	}
	if _, ok := doc["keep_me"]; ok {
		t.Fatal("keep_me should be removed when extras replaces unknowns")
	}
	if _, ok := doc["llm_new"]; !ok {
		t.Fatal("llm_new missing")
	}
}

func TestApplySecretPreserve(t *testing.T) {
	if got := ApplySecretPreserve(SecretRedacted, "sk-real"); got != "sk-real" {
		t.Fatalf("got %q", got)
	}
	if got := ApplySecretPreserve("sk-new", "sk-old"); got != "sk-new" {
		t.Fatalf("got %q", got)
	}
}

func TestSplitAppConfigDocument(t *testing.T) {
	cfg, extras, err := SplitAppConfigDocument([]byte(`{
		"llm":{"provider":"x"},
		"llm_previous_qwen":{"provider":"qwen"},
		"server":{"log_level":"info"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Provider != "x" || cfg.Server.LogLevel != "info" {
		t.Fatalf("cfg=%+v", cfg)
	}
	if _, ok := extras["llm_previous_qwen"]; !ok {
		t.Fatal("expected extra")
	}
	if _, ok := extras["llm"]; ok {
		t.Fatal("llm must not be extra")
	}
}
