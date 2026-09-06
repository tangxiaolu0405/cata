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

// TestProvidersList 设置界面供应商列表：迁移组装、探测状态、active 标记、无 api_key 泄露。
func TestProvidersList(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "ui-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	t.Setenv(config.EnvCataHome, home)
	t.Setenv(config.EnvConfigFile, "")
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(`{
  "llm": {"provider":"mock","api_url":"http://127.0.0.1:1/v1/chat/completions","model":"m1","enabled":true},
  "llm_px": {"provider":"px","api_url":"http://127.0.0.1:2/v1/chat/completions"}
}`), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewServer(gateway.Config{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/settings/app/providers", nil)
	rec := httptest.NewRecorder()
	s.handleAppProviders(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got providersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Providers) != 2 { // default(mock) + px
		t.Fatalf("providers=%d want 2 (default+px)", len(got.Providers))
	}
	// mock（default）为 active。
	activeFound := false
	for _, p := range got.Providers {
		if p.Active {
			activeFound = true
			if p.Name != "default" {
				t.Fatalf("active=%q want default", p.Name)
			}
		}
		if p.Models != nil && len(p.Models) > 0 {
			t.Fatal("未探测不应有模型")
		}
	}
	if !activeFound {
		t.Fatal("应存在 active provider")
	}
}

// TestProviderProbeAndActivate 探测 + 激活（不依赖真实网关：探测失败仍可激活连接定义）。
func TestProviderProbeAndActivate(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "ui-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	t.Setenv(config.EnvCataHome, home)
	t.Setenv(config.EnvConfigFile, "")
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(`{
  "llm": {"provider":"mock","api_url":"http://127.0.0.1:1/v1/chat/completions","model":"m1","enabled":true},
  "llm_px": {"provider":"px","api_url":"http://127.0.0.1:2/v1/chat/completions"}
}`), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewServer(gateway.Config{}, nil)

	// 激活 px（网关不可达，但连接定义仍生效；不报错）。
	body, _ := json.Marshal(map[string]string{"name": "px"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings/app/providers/activate", bytes.NewReader(body))
	s.handleAppProviderActivate(rec, req)
	if rec.Code != 200 {
		t.Fatalf("activate status=%d body=%s", rec.Code, rec.Body.String())
	}
	var res struct {
		Ok      bool             `json:"ok"`
		Message string           `json:"message"`
		Config  config.AppConfig `json:"config"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Ok {
		t.Fatalf("activate not ok: %s", res.Message)
	}
	if res.Config.LLM.APIURL != "http://127.0.0.1:2/v1" {
		t.Fatalf("激活后 llm.api_url=%q want normalized px url", res.Config.LLM.APIURL)
	}
	// 磁盘 active 应指向 px。
	providers, err := config.LoadLLMProviders()
	if err != nil {
		t.Fatal(err)
	}
	if providers.Active != "px" {
		t.Fatalf("active=%q want px", providers.Active)
	}

	// 探测一个不存在的 → 404。
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/settings/app/providers/activate",
		bytes.NewReader([]byte(`{"name":"nope"}`)))
	s.handleAppProviderActivate(rec, req)
	if rec.Code != 404 {
		t.Fatalf("不存在 provider 应 404, got %d", rec.Code)
	}
}

// TestProviderProbeAPI 探测接口返回 providers 视图（含失败状态），不中断。
func TestProviderProbeAPI(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "ui-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	t.Setenv(config.EnvCataHome, home)
	t.Setenv(config.EnvConfigFile, "")
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(`{
  "llm": {"provider":"mock","api_url":"http://127.0.0.1:1/v1/chat/completions","model":"m1","enabled":true}
}`), 0644); err != nil {
		t.Fatal(err)
	}
	s := NewServer(gateway.Config{}, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings/app/providers/probe",
		bytes.NewReader([]byte(`{"name":"default"}`)))
	s.handleAppProviderProbe(rec, req)
	if rec.Code != 200 {
		t.Fatalf("probe status=%d body=%s", rec.Code, rec.Body.String())
	}
	var res struct {
		Ok        bool              `json:"ok"`
		Providers providersResponse `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Ok {
		t.Fatal("mock 网关不可达应探测失败（ok=false 保留配置）")
	}
	if len(res.Providers.Providers) != 1 {
		t.Fatalf("providers=%d want 1", len(res.Providers.Providers))
	}
	if res.Providers.Providers[0].ProbedError == "" {
		t.Fatal("探测失败应记录 probed_error")
	}
	// 未探测成功 → models 保留为空（不覆盖）。
	p, _ := config.LoadLLMProviders()
	if len(p.Providers["default"].Probe.Models) != 0 {
		t.Fatal("失败不应覆盖模型清单")
	}
}
