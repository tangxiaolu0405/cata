// Package llm 的 provider 探测：自动发现网关支持的模型及其能力（text/image/audio），
// 写回 config.LLMProviders。策略：静态 /v1/models 清单 + 逐模型最小代价探针。
// 探测成功才覆盖；失败保留既有配置（调用方 SetProbeResult 处理）。
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cata/internal/cata/config"
)

// ProbeReport 一次 provider 探测的结果。
type ProbeReport struct {
	Models       []string
	Capabilities map[string]config.ModelCapCfg
}

// ProbeProvider 对指定连接定义执行完整探测：
//  1. GET {base}/models（OpenAI 兼容标准端点）→ 模型 id 清单
//  2. 对每个模型发最小 text 探针（max_tokens=1）确认可用
//  3. 对每个模型发 image 探针（1×1 PNG data URL）判定 image 支持
//
// 探针请求用独立临时 client，skipAppendLog（不写 llm.log）；任何单模型失败只标记
// unsupported，不中断整体。返回的 Capabilities 仅含明确判定的模型。
// 若无法连接 models 端点，返回 err（调用方据此保留既有配置）。
func ProbeProvider(ctx context.Context, apiURL, apiKey, apiFormat string, probeImage bool) (ProbeReport, error) {
	base := stripChatCompletionsPath(apiURL)
	models, err := fetchModels(ctx, base, apiKey)
	if err != nil {
		return ProbeReport{}, fmt.Errorf("probe models (%s): %w", base, err)
	}
	if len(models) == 0 {
		return ProbeReport{}, fmt.Errorf("probe models (%s): empty list", base)
	}

	caps := map[string]config.ModelCapCfg{}
	available := make([]string, 0, len(models))
	for _, m := range models {
		mods := []string{"text"}
		if probeImage && probeModelImage(ctx, base, apiKey, m) {
			mods = append(mods, "image")
		}
		caps[m] = config.ModelCapCfg{Modalities: mods}
		available = append(available, m)
	}
	return ProbeReport{Models: available, Capabilities: caps}, nil
}

// probeModelImage 发一个带 1×1 PNG 的 image 探针；2xx = 支持 image，
// 4xx/5xx/超时 = 不支持（不 panic、不中断）。
func probeModelImage(ctx context.Context, base, apiKey, model string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	body, _ := json.Marshal(map[string]interface{}{
		"model": model,
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
					{"type": "text", "text": "hi"},
					{"type": "image_url", "image_url": map[string]interface{}{"url": "data:image/png;base64," + png}},
				},
			},
		},
		"max_tokens": 1,
	})
	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost, joinURL(base, "chat/completions"), strings.NewReader(string(body)))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body) // 读空 body 以便连接复用
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// fetchModels 请求 {base}/models 拿模型 id 清单（OpenAI 兼容 data[].id）。
func fetchModels(ctx context.Context, base, apiKey string) ([]string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	u := joinURL(base, "models")
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	var ids []string
	for _, d := range out.Data {
		if strings.TrimSpace(d.ID) != "" {
			ids = append(ids, strings.TrimSpace(d.ID))
		}
	}
	return ids, nil
}

// stripChatCompletionsPath 把完整 /chat/completions 端点归一化为 base
// （与 config.NormalizeProviderURL 一致，落盘与探测同一口径）。
func stripChatCompletionsPath(apiURL string) string {
	return config.NormalizeProviderURL(apiURL)
}

// joinURL base + 子路径（base 可能已含 /v1）。
func joinURL(base, path string) string {
	b := strings.TrimRight(base, "/")
	p := strings.TrimLeft(path, "/")
	if strings.HasSuffix(b, "/v1") {
		return b + "/" + p
	}
	return b + "/" + p
}

// ProbeAndPersist 探测某 provider 并（仅成功时）写回 config。
// 失败时保留既有配置，只记录错误。返回探测报告与是否成功。
func ProbeAndPersist(ctx context.Context, name string, probeImage bool) (ProbeReport, bool) {
	providers, err := config.LoadLLMProviders()
	if err != nil {
		return ProbeReport{}, false
	}
	prov, ok := providers.Providers[name]
	if !ok {
		return ProbeReport{}, false
	}
	if strings.TrimSpace(prov.APIURL) == "" {
		return ProbeReport{}, false
	}
	rep, err := ProbeProvider(ctx, prov.APIURL, prov.APIKey, prov.APIFormat, probeImage)
	providers.SetProbeResult(name, rep.Models, rep.Capabilities, err)
	_ = config.SaveLLMProviders(providers)
	if err != nil {
		return ProbeReport{}, false
	}
	return rep, true
}

// probeLockPath 多进程互斥锁文件：避免 supervisor 同时拉起多个 agent 时重复/并发探测写 config。
func probeLockPath() string {
	return filepath.Join(config.CataHome(), "locks", "provider-probe.lock")
}

// acquireProbeLock 非阻塞拿启动探测锁（O_EXCL）。已存在且超 5 分钟视为残留，接管。
func acquireProbeLock() bool {
	path := probeLockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false
	}
	if fi, err := os.Stat(path); err == nil {
		if time.Since(fi.ModTime()) < 5*time.Minute {
			return false // 别的进程正在探测
		}
		_ = os.Remove(path) // 残留锁（崩溃/断电）
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return false
	}
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	_ = f.Close()
	return true
}

// releaseProbeLock 释放启动探测锁。
func releaseProbeLock() {
	_ = os.Remove(probeLockPath())
}

// AutoProbeStartup 常驻进程（agent / server）启动时的自动探测：
//   - 自动探测 llm_providers 中「已启用或为激活态」且未探测/探测过期（默认 24h）的 provider；
//   - 探测结果（models + capabilities）成功才写回；失败保留既有配置（SetProbeResult 语义）；
//   - 若成功探测的是激活态 provider，追加热应用 capabilities（+ chat_vision 候选）到 llm 主条目，
//     不覆盖用户显式配置的 api_url / model / api_key（连接定义由 ActivateProvider/CLI 负责）。
//
// 后台运行、非阻塞启动；多个 agent 进程并发时由文件锁互斥。
func AutoProbeStartup(probeImage bool) {
	if !acquireProbeLock() {
		return // 已有进程在探测
	}
	defer releaseProbeLock()

	providers, err := config.LoadLLMProviders()
	if err != nil {
		return
	}
	var names []string
	active := providers.Active
	for name, prov := range providers.Providers {
		if !prov.Enabled && name != active && name != "default" {
			continue
		}
		if !config.ProviderProbeExpired(prov.Probe.ProbedAt, 0) && prov.Probe.ProbedError == "" {
			continue // 已有较新且成功的探测结果，跳过
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		rep, ok := ProbeAndPersist(context.Background(), name, probeImage)
		if !ok {
			continue
		}
		// 热应用仅针对激活态（default 在 active 未设时代表当前 llm 主条目）。
		if name == active || (active == "" && name == "default") {
			hotApplyProbe(rep)
		}
	}
}

// hotApplyProbe 把成功探测的 capabilities 热应用到 llm 主条目（只补能力表与 chat_vision 候选，
// 不触碰连接定义/主模型——用户显式配置保持原样）。
func hotApplyProbe(rep ProbeReport) {
	if len(rep.Capabilities) == 0 {
		return
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		return
	}
	cfg.LLM.Capabilities = rep.Capabilities
	if _, ok := cfg.LLM.Models["chat_vision"]; !ok {
		if v := config.VisionCandidate(rep.Capabilities, cfg.LLM.Model); v != "" {
			if cfg.LLM.Models == nil {
				cfg.LLM.Models = map[string]string{}
			}
			cfg.LLM.Models["chat_vision"] = v
		}
	}
	if err := config.SaveConfig(cfg); err != nil {
		return
	}
	config.Config = cfg
}
