package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMigrateLegacyProvidersRegistersDefault 验证：
//  1. llm_* extras 迁移为 providers；
//  2. llm 主条目登记为 default provider；
//  3. active 补位为 default（当前激活态）；
//  4. 迁移后 llm_* extras 不再写盘（一次迁移）。
func TestMigrateLegacyProvidersRegistersDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvCataHome, dir)
	t.Setenv(EnvConfigFile, "")

	path := filepath.Join(dir, DefaultAppConfigName)
	initial := `{
  "llm": {
    "provider": "opencodego",
    "api_key": "sk-main",
    "api_url": "https://opencode.ai/zen/go/v1/chat/completions",
    "model": "deepseek-v4-flash",
    "enabled": true
  },
  "llm_deepseek": {
    "provider": "deepseek",
    "api_url": "https://api.deepseek.com/chat/completions"
  },
  "llm_ljllm": {
    "provider": "ljllm",
    "api_url": "http://8.134.252.166:8000/v1/chat/completions"
  },
  "llm_previous_qwen": { "provider": "qwen" }
}
`
	if err := os.WriteFile(path, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	providers, err := LoadLLMProviders()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"default", "deepseek", "ljllm", "previous_qwen"} {
		if _, ok := providers.Providers[want]; !ok {
			t.Fatalf("missing provider %q; got %v", want, providerNames(providers))
		}
	}
	if providers.Active != "default" {
		t.Fatalf("active=%q want default", providers.Active)
	}
	if got := providers.Providers["default"].APIURL; got != "https://opencode.ai/zen/go/v1/chat/completions" {
		t.Fatalf("default api_url=%q", got)
	}

	// 二次加载不再重复迁移（providers 数量不变，active 不变）。
	again, err := LoadLLMProviders()
	if err != nil {
		t.Fatal(err)
	}
	if !sameLLMProviders(again, providers) {
		t.Fatalf("second load changed providers: %v vs %v",
			providerNames(again), providerNames(providers))
	}

	// 迁移后 llm_deepseek 等 extras 应已从文档移除（并入 llm_providers）。
	raw, _ := os.ReadFile(path)
	s := string(raw)
	for _, k := range []string{"llm_deepseek", "llm_ljllm", "llm_previous_qwen"} {
		if strings.Contains(s, `"`+k+`"`) {
			t.Fatalf("legacy ext key %q still on disk after migration:\n%s", k, s)
		}
	}
}

// TestSetProbeResultFailureKeepsExisting 验证核心策略：探测失败不覆盖既有 models/capabilities，
// 只记录 ProbedError；成功才覆盖。
func TestSetProbeResultFailureKeepsExisting(t *testing.T) {
	p := &LLMProviders{Providers: map[string]LLMProvider{
		"deepseek": {
			Name: "deepseek",
			Probe: ProviderProbe{
				Models:       []string{"deepseek-v4-flash", "deepseek-v4-pro"},
				Capabilities: map[string]ModelCapCfg{"deepseek-v4-flash": {Modalities: []string{"text"}}},
				ProbedAt:     "2025-01-01T00:00:00Z",
			},
		},
	}}

	// 失败：保留既有。
	p.SetProbeResult("deepseek", nil, nil, os.ErrNotExist)
	if p.Providers["deepseek"].Probe.ProbedError == "" {
		t.Fatal("expected ProbedError recorded on failure")
	}
	if len(p.Providers["deepseek"].Probe.Models) != 2 {
		t.Fatalf("failure must keep models; got %v", p.Providers["deepseek"].Probe.Models)
	}
	if p.Providers["deepseek"].Probe.ProbedAt == "2025-01-01T00:00:00Z" {
		t.Fatal("ProbedAt should update even on failure (records attempt)")
	}

	// 成功：覆盖。
	p.SetProbeResult("deepseek", []string{"m1"}, map[string]ModelCapCfg{"m1": {Modalities: []string{"text", "image"}}}, nil)
	prov := p.Providers["deepseek"]
	if prov.Probe.ProbedError != "" {
		t.Fatalf("success must clear ProbedError; got %q", prov.Probe.ProbedError)
	}
	if len(prov.Probe.Models) != 1 || prov.Probe.Models[0] != "m1" {
		t.Fatalf("success must overwrite models; got %v", prov.Probe.Models)
	}
	if _, ok := prov.Probe.Capabilities["m1"]; !ok {
		t.Fatalf("success must overwrite capabilities; got %v", prov.Probe.Capabilities)
	}
	// 主模型未指定时取第一个。
	if prov.Model != "m1" {
		t.Fatalf("auto model=m1 expected; got %q", prov.Model)
	}
}

// TestProviderProbeExpired ttl 判定。
func TestProviderProbeExpired(t *testing.T) {
	if !ProviderProbeExpired("", 0) {
		t.Fatal("empty probed_at should be expired")
	}
	if !ProviderProbeExpired("not-a-time", 0) {
		t.Fatal("bogus probed_at should be treated as expired")
	}
	now := time.Now().Format(time.RFC3339)
	if ProviderProbeExpired(now, time.Hour) {
		t.Fatal("fresh probe should not be expired")
	}
	old := time.Now().Add(-48 * time.Hour).Format(time.RFC3339)
	if !ProviderProbeExpired(old, 24*time.Hour) {
		t.Fatal("stale probe should be expired")
	}
}
