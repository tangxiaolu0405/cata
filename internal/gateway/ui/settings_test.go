package ui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"cata/internal/cata/config"
	"cata/internal/gateway"
)

func TestSettingsAppPreserveExtras(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.EnvCataHome, dir)
	t.Setenv(config.EnvConfigFile, "")
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{
  "llm": {"provider":"deepseek","api_key":"sk-keep","enabled":true},
  "llm_previous_qwen": {"provider":"qwen","model":"qwen-turbo"}
}`), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewServer(gateway.Config{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/settings/app", nil)
	rec := httptest.NewRecorder()
	s.handleSettingsApp(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got appSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Extras["llm_previous_qwen"]; !ok {
		t.Fatal("extras missing llm_previous_qwen")
	}
	if got.Config.LLM.APIKey != config.SecretRedacted {
		t.Fatalf("api_key should be redacted, got %q", got.Config.LLM.APIKey)
	}

	got.Config.LLM.Provider = "mimo"
	body, _ := json.Marshal(appSettingsRequest{Config: got.Config, Extras: got.Extras})
	req = httptest.NewRequest(http.MethodPut, "/api/settings/app", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	s.handleSettingsApp(rec, req)
	if rec.Code != 200 {
		t.Fatalf("PUT status=%d body=%s", rec.Code, rec.Body.String())
	}

	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["llm_previous_qwen"]; !ok {
		t.Fatalf("llm_previous_qwen dropped: %s", raw)
	}
	var llm config.LLMConfig
	if err := json.Unmarshal(doc["llm"], &llm); err != nil {
		t.Fatal(err)
	}
	if llm.Provider != "mimo" || llm.APIKey != "sk-keep" {
		t.Fatalf("llm=%+v", llm)
	}
}
