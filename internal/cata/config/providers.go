package config

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"cata/internal/cata/clock"
)

// ProviderProbe 单个 provider 的自动探测结果（models 清单 + 各模型能力）。
// 探测策略：models 端点拿清单 → 逐模型发最小 text 探针 → 可选 image 探针。
// 探测成功才写 ProbedAt（成功字段优先于用户手写）；失败保留既有配置不覆盖。
type ProviderProbe struct {
	// Models 探测到的模型 id 清单（按网关返回）。
	Models []string `json:"models,omitempty"`
	// Capabilities 探测到的模型能力（仅含明确判定的；未探测的模型不在此表，运行时按仅 text）。
	Capabilities map[string]ModelCapCfg `json:"capabilities,omitempty"`
	// ProbedAt 最近一次成功探测时间（RFC3339）。空 = 探测失败或尚未探测。
	ProbedAt string `json:"probed_at,omitempty"`
	// ProbedError 最近一次探测失败原因（展示用；不写则配置继续有效）。
	ProbedError string `json:"probed_error,omitempty"`
}

// LLMProvider 一个 LLM 提供商：连接定义（写入/激活用）+ 自动探测结果。
type LLMProvider struct {
	// Name 提供商名（llm_providers.providers 的键；如 opencodego / deepseek）。
	Name string `json:"name,omitempty"`
	// Provider 展示标签（deepseek/qwen/gemini/…；不参与协议选择）。
	Provider string `json:"provider,omitempty"`
	// APIFormat openai | anthropic。
	APIFormat string `json:"api_format,omitempty"`
	// APIKey 该提供商密钥（空 = 回退环境变量）。
	APIKey string `json:"api_key,omitempty"`
	// APIURL 端点（base 或完整 chat/completions；运行时探测补全）。
	APIURL string `json:"api_url,omitempty"`
	// Model 用户/激活时选定的主模型（chat 模型；空 = 探测后取第一个 text 模型）。
	Model string `json:"model,omitempty"`
	// ModelsByRole 按角色覆盖（chat / chat_vision / evolution / worker）。
	ModelsByRole map[string]string `json:"models,omitempty"`
	// Enabled 是否启用（列入切换候选）。
	Enabled bool `json:"enabled,omitempty"`

	// Probe 自动探测结果。
	Probe ProviderProbe `json:"probe,omitempty"`
}

// LLMProviders 多 LLM 提供商注册表（config.json 顶层键 llm_providers）。
type LLMProviders struct {
	// Active 当前激活的提供商名（对应 providers 键；空 = 只用 llm 主条目）。
	Active string `json:"active,omitempty"`
	// Providers 全部已注册提供商（探测结果 / 连接定义）。
	Providers map[string]LLMProvider `json:"providers"`
}

// NormalizeProviderURL 把完整 chat 端点归一化为 base（剥到 /v1 截止）：
//
//	…/v1/chat/completions → …/v1
//	…/chat/completions    → …
//
// 已归一化的 URL 原样返回。探测 / 激活 / 落盘统一以 base 为准，
// 避免「带 /chat 尾缀的配置导致取不到 /models」类问题。
func NormalizeProviderURL(apiURL string) string {
	s := strings.TrimSpace(apiURL)
	s = strings.TrimRight(s, "/")
	const suf = "/chat/completions"
	if strings.HasSuffix(s, suf) {
		// 只剥 /chat/completions：…/v1/chat/completions → …/v1（保留 /v1）。
		return strings.TrimRight(strings.TrimSuffix(s, suf), "/")
	}
	return s
}

// normalizeProviderURLs 归一化所有 provider 的 api_url；返回是否发生改变。
func normalizeProviderURLs(p *LLMProviders) bool {
	if p == nil {
		return false
	}
	changed := false
	for name, prov := range p.Providers {
		if norm := NormalizeProviderURL(prov.APIURL); norm != prov.APIURL {
			prov.APIURL = norm
			p.Providers[name] = prov
			changed = true
		}
	}
	return changed
}

// LoadLLMProviders 读取 llm_providers 顶层键（来自磁盘文档，含未知键解析）。
// 同时迁移旧的 llm_<name> / llm_previous_qwen 备份条目为 provider（一次性）。
func LoadLLMProviders() (*LLMProviders, error) {
	cfg, extras, _, err := LoadAppConfigDocument()
	if err != nil {
		return nil, err
	}
	// 优先读已知结构（AppConfig.LLMProviders）。
	if cfg.LLMProviders != nil {
		orig := *cfg.LLMProviders // 浅拷贝：比较迁移/归一化是否产生实质变化
		// 迁移兜底：旧 llm_* extras 补充 + default（当前 llm 主条目）登记。
		if p := migrateLegacyProviders(extras, cfg.LLMProviders, cfg.LLM); p != nil {
			cfg.LLMProviders = p
		}
		// 统一归一化 api_url（含 /chat/completions 尾缀 → 剥到 base / /v1 截止）。
		norm := normalizeProviderURLs(cfg.LLMProviders)
		if norm || !sameLLMProviders(cfg.LLMProviders, &orig) {
			if err := persistLLMProviders(cfg.LLMProviders); err != nil {
				return nil, err
			}
		}
		return cfg.LLMProviders, nil
	}
	// 全新：从旧 llm_* 迁移（若有），否则空表。
	p := migrateLegacyProviders(extras, &LLMProviders{Providers: map[string]LLMProvider{}}, cfg.LLM)
	if p == nil {
		p = &LLMProviders{Providers: map[string]LLMProvider{}}
	}
	normalizeProviderURLs(p)
	if err := persistLLMProviders(p); err != nil {
		return nil, err
	}
	return p, nil
}

// migrateLegacyProviders 把磁盘上的 llm_<name> / llm_previous_qwen 顶层键转成 providers，
// 并把当前 llm 主条目登记为 default（激活态）。mainLLM 为文档里的 llm 主条目。
// 结构：{"provider":…,"api_format":…,"api_key":…,"api_url":…,"model":…}。
// 只并入原有键；已存在的 provider 名保持现有（含探测结果）不被覆盖。
func migrateLegacyProviders(extras map[string]json.RawMessage, cur *LLMProviders, mainLLM LLMConfig) *LLMProviders {
	if cur == nil {
		cur = &LLMProviders{Providers: map[string]LLMProvider{}}
	}
	if cur.Providers == nil {
		cur.Providers = map[string]LLMProvider{}
	}
	// 当前 llm 主条目作为 default provider 原型（不依赖全局 Config 状态）。
	base := LLMProvider{
		Name:      "default",
		Provider:  mainLLM.Provider,
		APIFormat: mainLLM.APIFormat,
		APIKey:    mainLLM.APIKey,
		APIURL:    mainLLM.APIURL,
		Model:     mainLLM.Model,
		Enabled:   true,
	}
	legacyKeys := make([]string, 0, len(extras))
	for k := range extras {
		if strings.HasPrefix(k, "llm_") {
			legacyKeys = append(legacyKeys, k)
		}
	}
	sort.Strings(legacyKeys)
	for _, k := range legacyKeys {
		name := strings.TrimPrefix(k, "llm_")
		if name == "" || name == "previous_qwen" {
			name = strings.TrimPrefix(k, "llm_") // previous_qwen → 保留原名作为 provider 名
		}
		if _, ok := cur.Providers[name]; ok {
			continue // 已注册：保留现有（含探测结果）
		}
		var raw struct {
			Provider  string `json:"provider"`
			APIFormat string `json:"api_format"`
			APIKey    string `json:"api_key"`
			APIURL    string `json:"api_url"`
			Model     string `json:"model"`
		}
		if err := json.Unmarshal(extras[k], &raw); err != nil {
			continue
		}
		p := LLMProvider{
			Name:      name,
			Provider:  raw.Provider,
			APIFormat: raw.APIFormat,
			APIKey:    raw.APIKey,
			APIURL:    raw.APIURL,
			Model:     raw.Model,
			Enabled:   true,
		}
		if p.Provider == "" {
			p.Provider = name
		}
		cur.Providers[name] = p
	}
	// active 未设且存在 unique/default 时补位。
	changed := false
	if strings.TrimSpace(base.APIURL) != "" || strings.TrimSpace(base.APIKey) != "" {
		// 始终登记「default」= 当前 llm 主条目（激活态），让启动自动探测有探测目标。
		if _, ok := cur.Providers["default"]; !ok {
			cur.Providers["default"] = base
			changed = true
		}
	}
	if cur.Active == "" {
		if _, ok := cur.Providers["default"]; ok {
			cur.Active = "default"
			changed = true
		} else if len(cur.Providers) == 1 {
			for n := range cur.Providers {
				cur.Active = n
			}
		}
	}
	_ = base // llm 主条目即 default 激活态
	if len(legacyKeys) == 0 && !changed {
		return nil
	}
	return cur
}

// sameLLMProviders 判断两次加载的注册表是否实质相同（避免无变化时重复持久化）。
func sameLLMProviders(a, b *LLMProviders) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Active != b.Active {
		return false
	}
	if len(a.Providers) != len(b.Providers) {
		return false
	}
	for n := range a.Providers {
		if _, ok := b.Providers[n]; !ok {
			return false
		}
	}
	return true
}

// SaveLLMProviders 写回 llm_providers 顶层键（作为已知键覆盖，保留其它 extras）。
func SaveLLMProviders(p *LLMProviders) error {
	cfg, extras, _, err := LoadAppConfigDocument()
	if err != nil {
		return err
	}
	cfg.LLMProviders = p
	return SaveAppConfigDocument(&cfg, extras)
}

// persistLLMProviders 迁移/归一化落盘：写回 llm_providers 已知键，清除已并入的旧 llm_* extras，
// 并把 llm 主条目 api_url 一并剥到 base（/v1 截止；运行时自动补全完整路径，见 api_url_resolved）。
func persistLLMProviders(p *LLMProviders) error {
	cfg, extras, _, err := LoadAppConfigDocument()
	if err != nil {
		return err
	}
	cfg.LLM.APIURL = NormalizeProviderURL(cfg.LLM.APIURL)
	for k := range extras {
		if strings.HasPrefix(k, "llm_") {
			delete(extras, k) // 已并入 llm_providers，从文档移除（一次性迁移）
		}
	}
	cfg.LLMProviders = p
	return SaveAppConfigDocument(&cfg, extras)
}

// SetProbeResult 记录某 provider 的探测结果。探测成功（err==nil）时覆盖模型/能力；
// 失败则保留既有配置，只记录错误信息与时间。
func (p *LLMProviders) SetProbeResult(name string, models []string, caps map[string]ModelCapCfg, err error) {
	if p.Providers == nil {
		p.Providers = map[string]LLMProvider{}
	}
	prov, ok := p.Providers[name]
	if !ok {
		prov = LLMProvider{Name: name, Enabled: true}
	}
	prov.Probe.ProbedAt = clock.RFC3339()
	if err != nil {
		prov.Probe.ProbedError = truncateStr(err.Error(), 200)
		// 失败不覆盖：models/capabilities 保留上次成功探测或用户手写。
	} else {
		prov.Probe.ProbedError = ""
		prov.Probe.Models = models
		prov.Probe.Capabilities = caps
		// 主模型未指定时取第一个 text 模型。
		if strings.TrimSpace(prov.Model) == "" {
			for _, m := range models {
				prov.Model = m
				break
			}
		}
	}
	p.Providers[name] = prov
}

// ActivateProvider 把某 provider 的连接定义 + 探测结果应用到 llm 主条目
// （切换生效：LLM 加载路径只读 llm.*，无需改其它代码）。
// 探测成功时 capabilities/models 由探测覆盖；探测失败/未探测则保留 llm 既有配置。
func ActivateProvider(name string) error {
	p, err := LoadLLMProviders()
	if err != nil {
		return err
	}
	prov, ok := p.Providers[name]
	if !ok {
		return fmt.Errorf("provider %q not found (registered: %v)", name, providerNames(p))
	}
	if strings.TrimSpace(prov.APIURL) == "" {
		return fmt.Errorf("provider %q has no api_url", name)
	}
	cfg := Config
	if cfg == nil {
		cfg, err = LoadConfig()
		if err != nil {
			return err
		}
	}
	// 连接定义。
	cfg.LLM.APIFormat = firstNonEmpty(prov.APIFormat, cfg.LLM.APIFormat, "openai")
	cfg.LLM.APIURL = prov.APIURL
	cfg.LLM.APIKey = prov.APIKey
	cfg.LLM.Provider = prov.Provider
	if prov.Model != "" {
		cfg.LLM.Model = prov.Model
	}
	if len(prov.ModelsByRole) > 0 {
		if cfg.LLM.Models == nil {
			cfg.LLM.Models = map[string]string{}
		}
		for k, v := range prov.ModelsByRole {
			cfg.LLM.Models[k] = v
		}
	}
	// 探测结果（成功才覆盖 capabilities）。
	if prov.Probe.ProbedAt != "" && prov.Probe.ProbedError == "" {
		if len(prov.Probe.Capabilities) > 0 {
			cfg.LLM.Capabilities = prov.Probe.Capabilities
		}
		// 视觉候选：探测到支持 image 的模型且未显式配置 chat_vision → 自动设。
		if _, ok := cfg.LLM.Models["chat_vision"]; !ok {
			if v := visionCandidate(prov.Probe.Capabilities, prov.Model); v != "" {
				if cfg.LLM.Models == nil {
					cfg.LLM.Models = map[string]string{}
				}
				cfg.LLM.Models["chat_vision"] = v
			}
		}
	}
	p.Active = name
	cfg.LLMProviders = p
	if err := SaveConfig(cfg); err != nil {
		return err
	}
	Config = cfg
	return nil
}

// SetProviderModel 更新某 provider 的主模型（用户显式指定；不触发探测）。
func SetProviderModel(name, model string) error {
	p, err := LoadLLMProviders()
	if err != nil {
		return err
	}
	prov, ok := p.Providers[name]
	if !ok {
		return fmt.Errorf("provider %q not found", name)
	}
	prov.Model = model
	p.Providers[name] = prov
	return SaveLLMProviders(p)
}

// VisionCandidate 从探测到的能力表里找一个支持 image 的模型（优先当前 chat 模型）。
// 导出供 llm 探测热应用与 CLI/TUI 使用。
func VisionCandidate(caps map[string]ModelCapCfg, current string) string {
	return visionCandidate(caps, current)
}

// visionCandidate 从探测到的能力表里找一个支持 image 的模型（优先当前 chat 模型）。
func visionCandidate(caps map[string]ModelCapCfg, current string) string {
	for _, m := range []string{current} {
		if c, ok := caps[m]; ok && slicesContains(c.Modalities, "image") {
			return m
		}
	}
	var names []string
	for m, c := range caps {
		if slicesContains(c.Modalities, "image") {
			names = append(names, m)
		}
	}
	sort.Strings(names)
	if len(names) > 0 {
		return names[0]
	}
	return ""
}

// ProviderProbedAt 返回某 provider 最近成功探测时间（空 = 未探测/失败）。
func (p *LLMProviders) ProviderProbedAt(name string) string {
	if p == nil || p.Providers == nil {
		return ""
	}
	return p.Providers[name].Probe.ProbedAt
}

func providerNames(p *LLMProviders) []string {
	var out []string
	for n := range p.Providers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func slicesContains(a []string, v string) bool {
	for _, x := range a {
		if x == v {
			return true
		}
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ProviderProbeExpired 探测结果是否过期（需重探）。默认 24h。
func ProviderProbeExpired(probedAt string, ttl time.Duration) bool {
	if probedAt == "" {
		return true
	}
	ts, err := time.Parse(time.RFC3339, probedAt)
	if err != nil {
		return true
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return time.Since(ts) > ttl
}
