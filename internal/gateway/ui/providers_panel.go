package ui

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"cata/internal/cata/config"
	"cata/internal/llm"
)

// providerView 设置界面展示的供应商视图（api_key 不返回）。
type providerView struct {
	Name         string                        `json:"name"`
	Provider     string                        `json:"provider"`
	APIFormat    string                        `json:"api_format"`
	APIURL       string                        `json:"api_url"`
	Model        string                        `json:"model"`
	Enabled      bool                          `json:"enabled"`
	Active       bool                          `json:"active"`
	ProbedAt     string                        `json:"probed_at,omitempty"`
	ProbedError  string                        `json:"probed_error,omitempty"`
	Models       []string                      `json:"models,omitempty"`
	Capabilities map[string]config.ModelCapCfg `json:"capabilities,omitempty"`
}

type providersResponse struct {
	Active    string         `json:"active"`
	Providers []providerView `json:"providers"`
}

// providersList 读 llm_providers 并转视图（按名排序，active 置顶标记）。
func providersList() providersResponse {
	p, err := config.LoadLLMProviders()
	if err != nil {
		return providersResponse{}
	}
	resp := providersResponse{Active: p.Active}
	names := make([]string, 0, len(p.Providers))
	for n := range p.Providers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		prov := p.Providers[n]
		resp.Providers = append(resp.Providers, providerView{
			Name:         prov.Name,
			Provider:     prov.Provider,
			APIFormat:    prov.APIFormat,
			APIURL:       prov.APIURL,
			Model:        prov.Model,
			Enabled:      prov.Enabled,
			Active:       n == p.Active,
			ProbedAt:     prov.Probe.ProbedAt,
			ProbedError:  prov.Probe.ProbedError,
			Models:       prov.Probe.Models,
			Capabilities: prov.Probe.Capabilities,
		})
	}
	return resp
}

// handleAppProviders GET 供应商列表（含探测状态）。
func (s *Server) handleAppProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	writeJSON(w, providersList())
}

func decodeProviderAction(w http.ResponseWriter, r *http.Request) map[string]string {
	var body struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return nil
	}
	return map[string]string{"name": strings.TrimSpace(body.Name), "model": strings.TrimSpace(body.Model)}
}

func providerNotFound(w http.ResponseWriter, name string) {
	http.Error(w, "provider not found: "+name, 404)
}

// handleAppProviderProbe POST 探测某供应商并写回（成功才覆盖能力表）。
func (s *Server) handleAppProviderProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	act := decodeProviderAction(w, r)
	if act == nil {
		return
	}
	name := act["name"]
	rep, ok := llm.ProbeAndPersist(r.Context(), name, true)
	if !ok {
		// 失败不覆盖：返回更新后的视图供前端展示 probed_error。
		writeJSON(w, map[string]any{"ok": false, "providers": providersList(), "name": name})
		return
	}
	_ = rep
	writeJSON(w, map[string]any{"ok": true, "providers": providersList(), "name": name})
}

// handleAppProviderActivate POST 激活供应商（可指定模型）。
// 缺探测/过期/失败时先自动补探（失败仍激活连接定义，能力表沿用既有）。
func (s *Server) handleAppProviderActivate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	act := decodeProviderAction(w, r)
	if act == nil {
		return
	}
	name := act["name"]
	providers, err := config.LoadLLMProviders()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	prov, ok := providers.Providers[name]
	if !ok {
		providerNotFound(w, name)
		return
	}
	if config.ProviderProbeExpired(prov.Probe.ProbedAt, 0) || prov.Probe.ProbedError != "" {
		llm.ProbeAndPersist(r.Context(), name, true)
	}
	if act["model"] != "" {
		if err := config.SetProviderModel(name, act["model"]); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	if err := config.ActivateProvider(name); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if _, err := config.LoadConfig(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	cfg, extras, path, err := config.LoadAppConfigDocument()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{
		"ok":        true,
		"message":   "已激活 " + name,
		"providers": providersList(),
		"config":    config.RedactConfig(&cfg),
		"extras":    extras,
		"path":      path,
	})
}
