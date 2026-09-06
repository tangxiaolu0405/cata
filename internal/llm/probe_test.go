package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cata/internal/cata/config"
)

// TestProbeProviderModelsAndImage 验证探测：models 清单 + text 可用 + image 判定。
func TestProbeProviderModelsAndImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"text-model"},{"id":"vision-model"},{"id":"broken-model"}]}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			bodyBytes, _ := io.ReadAll(r.Body)
			var req map[string]any
			_ = json.Unmarshal(bodyBytes, &req)
			model, _ := req["model"].(string)
			containsImage := strings.Contains(string(bodyBytes), "image_url")
			if model == "broken-model" {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			if model == "text-model" && containsImage {
				http.Error(w, "image not supported", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
		}
	}))
	defer srv.Close()

	// 用带 chat/completions 的完整 URL 调用探测（内部归一化 base）。
	rep, err := ProbeProvider(context.Background(), srv.URL+"/v1/chat/completions", "key", "openai", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Models) != 3 {
		t.Fatalf("models=%v want 3", rep.Models)
	}
	// vision-model 应有 image；text-model 仅 text（image 探针被 400 判定不支持）。
	if !contains(rep.Capabilities["vision-model"].Modalities, "image") {
		t.Fatalf("vision-model caps=%v want image", rep.Capabilities["vision-model"])
	}
	if contains(rep.Capabilities["text-model"].Modalities, "image") {
		t.Fatalf("text-model caps=%v want text only", rep.Capabilities["text-model"])
	}
	// broken-model 也应出现在清单（探针失败不中断），仅 text。
	if _, ok := rep.Capabilities["broken-model"]; !ok {
		t.Fatal("broken-model should still be listed (probe failure not fatal)")
	}
}

func TestStripChatCompletionsPath(t *testing.T) {
	cases := map[string]string{
		"https://api.deepseek.com/chat/completions":                "https://api.deepseek.com",
		"https://opencode.ai/zen/go/v1/chat/completions":           "https://opencode.ai/zen/go/v1",
		"https://opencode.ai/zen/go/v1":                            "https://opencode.ai/zen/go/v1",
		"https://generativelanguage.googleapis.com/v1beta/openai/": "https://generativelanguage.googleapis.com/v1beta/openai",
	}
	for in, want := range cases {
		if got := stripChatCompletionsPath(in); got != want {
			t.Fatalf("strip(%q)=%q want %q", in, got, want)
		}
	}
}

func contains(a []string, v string) bool {
	for _, x := range a {
		if x == v {
			return true
		}
	}
	return false
}

// TestAutoProbeStartup 验证常驻进程启动自动探测：
//  1. 缺探测的 enabled provider（含 default=当前激活）自动探测写回 probe；
//  2. active provider 探测后热应用 capabilities + chat_vision 到 llm 主条目；
//  3. 探测成功才覆盖（ProbedError 空），失败保留既有。
func TestAutoProbeStartup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"vision-model"},{"id":"text-model"}]}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			bodyBytes, _ := io.ReadAll(r.Body)
			var req map[string]any
			_ = json.Unmarshal(bodyBytes, &req)
			model, _ := req["model"].(string)
			if strings.Contains(string(bodyBytes), "image_url") {
				if model == "vision-model" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
					return
				}
				http.Error(w, "no image", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	t.Setenv(config.EnvCataHome, dir)
	t.Setenv(config.EnvConfigFile, "")

	path := filepath.Join(dir, config.DefaultAppConfigName)
	initial := `{
  "llm": {
    "provider": "mock",
    "api_key": "sk-main",
    "api_url": "` + srv.URL + `/v1/chat/completions",
    "model": "text-model",
    "enabled": true,
    "models": { "chat": "text-model" }
  }
}
`
	if err := os.WriteFile(path, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	AutoProbeStartup(true)

	providers, err := config.LoadLLMProviders()
	if err != nil {
		t.Fatal(err)
	}
	def, ok := providers.Providers["default"]
	if !ok {
		t.Fatalf("default provider not registered; got %v", providerNamesForTest(providers))
	}
	if len(def.Probe.Models) != 2 {
		t.Fatalf("default probe models=%v want 2", def.Probe.Models)
	}
	if def.Probe.ProbedError != "" {
		t.Fatalf("probe failed unexpectedly: %s", def.Probe.ProbedError)
	}

	// 热应用检查：llm 主条目 capabilities + chat_vision。
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.LLM.Capabilities["vision-model"]; !ok {
		t.Fatalf("llm.capabilities not hot-applied; got %v", cfg.LLM.Capabilities)
	}
	if cfg.LLM.Models["chat_vision"] != "vision-model" {
		t.Fatalf("chat_vision=%q want vision-model", cfg.LLM.Models["chat_vision"])
	}
	// llm 连接定义不应被热应用改动。
	if cfg.LLM.Model != "text-model" {
		t.Fatalf("hot-apply must not touch llm.model; got %q", cfg.LLM.Model)
	}
}

func providerNamesForTest(p *config.LLMProviders) []string {
	var out []string
	for n := range p.Providers {
		out = append(out, n)
	}
	return out
}
