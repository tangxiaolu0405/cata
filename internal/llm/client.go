package llm

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
)

const (
	// DefaultOpenAIAPIURL OpenAI API 地址
	DefaultOpenAIAPIURL = "https://api.openai.com/v1/chat/completions"
	// DefaultModel 默认模型
	DefaultModel = "gpt-3.5-turbo"
	// DefaultMaxTokens 默认最大 token 数
	DefaultMaxTokens = 2000
	// DefaultTimeout 默认超时时间（非流式 / 等响应头）
	DefaultTimeout = 180 * time.Second
	// MinStreamTimeout 流式整段请求下限（多轮 tool + 长生成）
	MinStreamTimeout = 10 * time.Minute

	// 注入 API 的 brain 节选（core/workflow/hot）字节上限，减轻每轮请求的输入 token
	maxBrainExcerptBytesPerFile = 6500
	maxBrainExcerptBytesTotal   = 20000
	// boot-leader 正文码点上限（单文件过大时截断）
	maxBootLeaderRunes = 10000
)

// truncateRunes 按 Unicode 码点截断（仅用于发往 API 的 boot-leader 体积控制，不用于 llm.log）。
func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 || s == "" {
		return s
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "\n…(truncated)"
}

// cloneMessagesForLLMLog 深拷贝消息列表，供 llm.log 原样记录（不截断、不压缩正文）。
func cloneMessagesForLLMLog(msgs []Message) []Message {
	out := make([]Message, len(msgs))
	for i, m := range msgs {
		out[i] = m
		if len(m.ToolCalls) > 0 {
			out[i].ToolCalls = append([]ToolCall(nil), m.ToolCalls...)
		}
	}
	return out
}

// compactMessageContentForAPI 压缩发往模型的 system/user 正文中连续空行，降低换行 token。
func compactMessageContentForAPI(msgs []Message) []Message {
	if len(msgs) == 0 {
		return msgs
	}
	out := make([]Message, len(msgs))
	for i, m := range msgs {
		out[i] = m
		switch m.Role {
		case "system", "user":
			if m.Content != "" {
				out[i].Content = brain.CompactExcessiveNewlines(m.Content)
			}
		}
	}
	return out
}

// Client LLM 客户端
type Client struct {
	apiKey           string
	apiURL           string // 当前使用的 endpoint（可能已探测记住）
	apiURLConfigured string // 用户配置原样（trim 后）
	model            string
	providerLabel    string // 展示标签，不参与协议选择
	apiFormat        string
	maxTokens        int
	timeout          time.Duration
	httpClient       *http.Client
	streamHTTPClient *http.Client
	adapter          APIAdapter
	card             *RoleCard // 角色卡片：身份 + 协议 + 采样 + 注入策略（NewClientForRole 挂载）
}

func resolveInitialAPIURL(apiFormat, configured string) (active, configuredOut string) {
	configuredOut = TrimAPIURL(configured)
	if configuredOut == "" {
		return "", ""
	}
	if cached := LookupResolvedAPIURL(configuredOut); cached != "" {
		return cached, configuredOut
	}
	// 尚未记住时先用配置原样；缺路径则首次请求会按 CandidateAPIURLs 再试。
	_ = apiFormat
	return configuredOut, configuredOut
}

func (c *Client) apiURLCandidates() []string {
	if c == nil {
		return nil
	}
	base := c.apiURLConfigured
	if base == "" {
		base = c.apiURL
	}
	cands := CandidateAPIURLs(c.apiFormat, base)
	// 若已记住且仍在候选中，优先只用记住的（避免每次多打一次）。
	if cached := LookupResolvedAPIURL(base); cached != "" {
		return []string{cached}
	}
	return cands
}

func (c *Client) commitAPIURL(u string) {
	u = TrimAPIURL(u)
	if u == "" || c == nil {
		return
	}
	c.apiURL = u
	cfg := c.apiURLConfigured
	if cfg == "" {
		cfg = u
	}
	RememberResolvedAPIURL(cfg, u)
}

func streamRoundTimeout(base time.Duration) time.Duration {
	d := base * 10
	if d < MinStreamTimeout {
		return MinStreamTimeout
	}
	return d
}

func configureHTTPTransport(timeout time.Duration) *http.Transport {
	tr := &http.Transport{
		ResponseHeaderTimeout: timeout,
	}
	proxyURL := os.Getenv("HTTP_PROXY")
	if proxyURL == "" {
		proxyURL = os.Getenv("HTTPS_PROXY")
	}
	if proxyURL == "" {
		proxyURL = os.Getenv("ALL_PROXY")
	}
	if proxyURL != "" {
		parsedProxy, err := url.Parse(proxyURL)
		if err == nil {
			if parsedProxy.Scheme == "http" || parsedProxy.Scheme == "https" {
				tr.Proxy = http.ProxyURL(parsedProxy)
				log.Printf("Using HTTP proxy: %s", proxyURL)
			} else if parsedProxy.Scheme == "socks5" {
				tr.Proxy = http.ProxyURL(parsedProxy)
				log.Printf("WARNING: SOCKS5 proxy detected (%s) but not fully supported. If you see EOF errors, try: 1) Start your proxy server, 2) Use HTTP proxy instead, or 3) Unset proxy env vars", proxyURL)
			}
		}
	}
	return tr
}

func newHTTPClientPair(timeout time.Duration) (*http.Client, *http.Client) {
	// 流式/慢推理：响应头可能很晚才到（代理等模型想完再回），header 超时与整段流超时对齐。
	regularTr := configureHTTPTransport(timeout)
	streamTO := streamRoundTimeout(timeout)
	streamTr := configureHTTPTransport(streamTO)
	regular := &http.Client{Timeout: timeout, Transport: regularTr}
	stream := &http.Client{Timeout: streamTO, Transport: streamTr}
	return regular, stream
}

// loadBootLeaderPrompt 返回主 chat 角色卡片的身份正文（历史名保留，供日志标注与无卡片兜底）。
// 实际出站由 assembleSystemForRole 按 Client.card 组装；运行时覆盖文件编辑后立即生效。
func loadBootLeaderPrompt() string {
	if card, err := CardForRole(RoleChat); err == nil {
		return truncateRunes(strings.TrimSpace(card.Body), maxBootLeaderRunes)
	}
	return ""
}

// ensureRetrievedMemorySystemForCtx 在身份之后插入与当前请求相关的记忆检索块。
// 幂等（已注入则跳过）；minimal 档（worker）不检索。
func ensureRetrievedMemorySystemForCtx(ctx context.Context, msgs []Message, profile brain.PromptProfile) []Message {
	if brain.ProfileRank(profile) < 1 {
		return msgs
	}
	for _, m := range msgs {
		if m.Role == "system" && strings.HasPrefix(strings.TrimSpace(m.Content), brain.RetrievedMemorySystemPrefix) {
			return msgs
		}
	}
	query := lastUserMessageContent(msgs)
	if strings.TrimSpace(query) == "" {
		return msgs
	}
	block := brain.RetrievedMemorySystemBlock(ctx, profile, query)
	if strings.TrimSpace(block) == "" {
		return msgs
	}
	if len(msgs) >= 1 && msgs[0].Role == "system" {
		out := make([]Message, 0, len(msgs)+1)
		out = append(out, msgs[0])
		out = append(out, Message{Role: "system", Content: block})
		out = append(out, msgs[1:]...)
		return out
	}
	return append([]Message{{Role: "system", Content: block}}, msgs...)
}

// lastUserMessageContent 从后往前找最后一条 user 消息正文（检索 query）。
func lastUserMessageContent(msgs []Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	return ""
}

// assembleSystemForRole 按角色卡片组装 system 消息：
// 身份（卡片 body）→ 相关记忆检索块 → brain 节选（coalesce 后仍保持该顺序）。
// 未挂卡片时回退到主 chat 角色卡片身份。
func (c *Client) assembleSystemForRole(ctx context.Context, messages []Message, profile brain.PromptProfile) []Message {
	body := ""
	if c.card != nil {
		body = strings.TrimSpace(c.card.Body)
	}
	if body == "" {
		body = loadBootLeaderPrompt()
	}
	out := messages
	if body != "" {
		already := len(messages) > 0 && messages[0].Role == "system" && strings.TrimSpace(messages[0].Content) == body
		if !already {
			out = make([]Message, 0, len(messages)+1)
			out = append(out, Message{Role: "system", Content: body})
			out = append(out, messages...)
		}
	}
	out = ensureRetrievedMemorySystemForCtx(ctx, out, profile)
	return ensureCataBrainExcerptSystemForCtx(ctx, out, profile)
}

// ensureCataBrainExcerptSystem 在 boot-leader 之后插入路径块 + 脑子节选（若尚未存在）。
func ensureCataBrainExcerptSystem(msgs []Message) []Message {
	return ensureCataBrainExcerptSystemFor(msgs, brain.ActivePromptProfile())
}

func ensureCataBrainExcerptSystemFor(msgs []Message, profile brain.PromptProfile) []Message {
	return ensureCataBrainExcerptSystemForCtx(nil, msgs, profile)
}

// ensureCataBrainExcerptSystemForCtx 与 For 版相同，但节选按 ctx 中 ChatContext 组装。
func ensureCataBrainExcerptSystemForCtx(ctx context.Context, msgs []Message, profile brain.PromptProfile) []Message {
	for _, m := range msgs {
		if m.Role != "system" {
			continue
		}
		c := strings.TrimSpace(m.Content)
		if strings.HasPrefix(c, brain.TerminalPathsSystemPrefix) ||
			brain.BrainExcerptInjected(c) {
			return msgs
		}
	}
	perFile, total := brainExcerptLimitsFor(profile)
	ext := brain.TerminalBrainSystemExtensionForContext(ctx, profile, perFile, total)
	if strings.TrimSpace(ext) == "" {
		return msgs
	}
	pack := ext
	if len(msgs) >= 1 && msgs[0].Role == "system" {
		out := make([]Message, 0, len(msgs)+1)
		out = append(out, msgs[0])
		out = append(out, Message{Role: "system", Content: pack})
		out = append(out, msgs[1:]...)
		return out
	}
	return append([]Message{{Role: "system", Content: pack}}, msgs...)
}

// coalesceSystemMessagesForAPI 把所有 system 合并成一条并置于 messages 最前。
// 适配 vLLM / 部分 OpenAI 兼容网关对「system 只能在开头（且只要一条）」的校验。
func coalesceSystemMessagesForAPI(msgs []Message) []Message {
	if len(msgs) == 0 {
		return msgs
	}
	var systems []string
	rest := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "system" {
			if c := strings.TrimSpace(m.Content); c != "" {
				systems = append(systems, c)
			}
			continue
		}
		rest = append(rest, m)
	}
	if len(systems) == 0 {
		return rest
	}
	if len(systems) == 1 {
		// 已在最前且仅一条时保持原序（rest 前若本来就有这条，重建即可）
		out := make([]Message, 0, len(rest)+1)
		out = append(out, Message{Role: "system", Content: systems[0]})
		out = append(out, rest...)
		return out
	}
	out := make([]Message, 0, len(rest)+1)
	out = append(out, Message{Role: "system", Content: strings.Join(systems, "\n\n")})
	out = append(out, rest...)
	return out
}

func brainExcerptLimits() (perFile, total int) {
	return brainExcerptLimitsFor(brain.ActivePromptProfile())
}

func brainExcerptLimitsFor(profile brain.PromptProfile) (perFile, total int) {
	switch brain.ProfileRank(profile) {
	case 0:
		return 800, 2000
	case 1:
		return 3000, 8000
	default:
		return maxBrainExcerptBytesPerFile, maxBrainExcerptBytesTotal
	}
}

// resolveModelForRole 根据全局配置与角色解析应使用的模型名称。
// 优先级：cfg.Models[role] -> cfg.Models["default"] -> cfg.Model -> 由 NewClientFromConfig 内部根据环境变量与 Provider 决定。
func resolveModelForRole(cfg config.LLMConfig, role Role) string {
	if cfg.Models != nil {
		if model, ok := cfg.Models[string(role)]; ok && strings.TrimSpace(model) != "" {
			return strings.TrimSpace(model)
		}
		if model, ok := cfg.Models[string(RoleDefault)]; ok && strings.TrimSpace(model) != "" {
			return strings.TrimSpace(model)
		}
	}

	if strings.TrimSpace(cfg.Model) != "" {
		return strings.TrimSpace(cfg.Model)
	}

	// 为空时交由 NewClientFromConfig 使用环境变量与 Provider 默认值决定
	return ""
}

// NewClientForRole 使用全局配置和角色创建 LLM 客户端。
// - 当配置文件启用 LLM 时，从 config.Config.LLM 读取 Provider/APIURL/APIKey/MaxTokens/Timeout，并按角色解析模型名。
// - 当配置未启用或尚未加载时，回退到 NewClient（环境变量与默认策略）。
func NewClientForRole(role Role) (*Client, error) {
	var c *Client
	var err error
	if config.Config != nil && config.Config.LLM.Enabled {
		llmCfg := config.Config.LLM
		model := resolveModelForRole(llmCfg, role)
		c, err = NewClientFromLLMConfig(llmCfg, model)
	} else {
		c, err = NewClient()
	}
	if err != nil {
		return nil, err
	}
	// 惰性 seed 运行时角色卡片模板（文件不存在才写）；失败不阻塞（embed 兜底）。
	if err := EnsureRoleCards(); err != nil {
		log.Printf("role cards seed: %v", err)
	}
	if err := c.attachRoleCard(role); err != nil {
		return nil, err
	}
	return c, nil
}

// attachRoleCard 加载并挂载角色卡片。
func (c *Client) attachRoleCard(role Role) error {
	card, err := CardForRole(role)
	if err != nil {
		return err
	}
	c.card = card
	return nil
}

// NewClientFromLLMConfig 从 LLMConfig 片段创建客户端（api_format 决定协议，provider 仅标签）。
func NewClientFromLLMConfig(llmCfg config.LLMConfig, model string) (*Client, error) {
	return NewClientFromConfig(
		llmCfg.Provider,
		llmCfg.APIFormat,
		llmCfg.APIKey,
		llmCfg.APIURL,
		model,
		llmCfg.MaxTokens,
		time.Duration(llmCfg.Timeout)*time.Second,
	)
}

// NewClient 创建新的 LLM 客户端（从环境变量或配置读取）
func NewClient() (*Client, error) {
	return NewClientFromConfig("", "", "", "", "", 0, 0)
}

// NewClientFromConfig 从配置创建客户端。providerLabel 仅作展示；apiFormat 为 openai|anthropic。
func NewClientFromConfig(providerLabel, apiFormat, apiKey, apiURL, model string, maxTokens int, timeout time.Duration) (*Client, error) {
	if apiKey == "" {
		apiKey = os.Getenv("LLM_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("OPENAI_API_KEY")
		}
		if apiKey == "" {
			apiKey = os.Getenv("DEEPSEEK_API_KEY")
		}
		if apiKey == "" {
			apiKey = os.Getenv("DASHSCOPE_API_KEY")
		}
		if apiKey == "" {
			apiKey = os.Getenv("ANTHROPIC_API_KEY")
		}
		if apiKey == "" {
			return nil, fmt.Errorf("LLM API key not set (set LLM_API_KEY or configure llm.api_key)")
		}
	}

	if providerLabel == "" {
		providerLabel = os.Getenv("LLM_PROVIDER")
	}

	if apiURL == "" {
		apiURL = os.Getenv("LLM_API_URL")
		if apiURL == "" {
			apiURL = os.Getenv("OPENAI_API_URL")
		}
	}

	apiFormat = ResolveAPIFormat(apiFormat, apiURL, providerLabel)

	if apiURL == "" {
		apiURL = defaultAPIURLForFormat(apiFormat)
	}
	activeURL, configuredURL := resolveInitialAPIURL(apiFormat, apiURL)

	if model == "" {
		model = os.Getenv("LLM_MODEL")
		if model == "" {
			model = os.Getenv("OPENAI_MODEL")
		}
		if model == "" {
			model = defaultModelForFormat(apiFormat)
		}
	}

	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	httpClient, streamClient := newHTTPClientPair(timeout)

	log.Printf("Creating LLM Client: label=%s format=%s URL=%s (configured=%s) Model=%s APIKey present=%v timeout=%s stream_timeout=%s",
		providerLabel, apiFormat, activeURL, configuredURL, model, apiKey != "", timeout, streamRoundTimeout(timeout))

	return &Client{
		apiKey:           apiKey,
		apiURL:           activeURL,
		apiURLConfigured: configuredURL,
		model:            model,
		providerLabel:    providerLabel,
		apiFormat:        apiFormat,
		maxTokens:        maxTokens,
		timeout:          timeout,
		adapter:          GetAPIAdapter(apiFormat),
		httpClient:       httpClient,
		streamHTTPClient: streamClient,
	}, nil
}

func defaultAPIURLForFormat(apiFormat string) string {
	switch ResolveAPIFormat(apiFormat, "", "") {
	case APIFormatAnthropic:
		return DefaultAnthropicURL
	default:
		return DefaultOpenAIAPIURL
	}
}

func defaultModelForFormat(apiFormat string) string {
	switch ResolveAPIFormat(apiFormat, "", "") {
	case APIFormatAnthropic:
		return "claude-sonnet-4-20250514"
	default:
		return DefaultModel
	}
}

// NewClientWithConfig 使用自定义配置创建客户端
func NewClientWithConfig(apiKey, apiURL, model string, maxTokens int, timeout time.Duration) *Client {
	return NewClientWithAPIFormat(apiKey, apiURL, model, APIFormatOpenAI, maxTokens, timeout)
}

// NewClientWithAPIFormat 指定 api_format 创建客户端（测试用）。
func NewClientWithAPIFormat(apiKey, apiURL, model, apiFormat string, maxTokens int, timeout time.Duration) *Client {
	if apiURL == "" {
		apiURL = defaultAPIURLForFormat(apiFormat)
	}
	if model == "" {
		model = defaultModelForFormat(apiFormat)
	}
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	httpClient, streamClient := newHTTPClientPair(timeout)
	format := ResolveAPIFormat(apiFormat, apiURL, "")
	activeURL, configuredURL := resolveInitialAPIURL(format, apiURL)
	return &Client{
		apiKey:           apiKey,
		apiURL:           activeURL,
		apiURLConfigured: configuredURL,
		model:            model,
		apiFormat:        format,
		maxTokens:        maxTokens,
		timeout:          timeout,
		adapter:          GetAPIAdapter(format),
		httpClient:       httpClient,
		streamHTTPClient: streamClient,
	}
}

// NewClientWithProvider 兼容旧名；provider 参数现作 api_format 或标签，默认 openai。
func NewClientWithProvider(apiKey, apiURL, model, provider string, maxTokens int, timeout time.Duration) *Client {
	format := ResolveAPIFormat("", apiURL, provider)
	return NewClientWithAPIFormat(apiKey, apiURL, model, format, maxTokens, timeout)
}

// Message 消息结构（兼容 OpenAI Chat：含 tool_calls / tool 角色）
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
	// ReasoningContent DeepSeek 思考模式 CoT；有 tool_calls 时下一轮必须原样回传。
	ReasoningContent string `json:"reasoning_content,omitempty"`
	// 助手消息携带的工具调用（发给 API 的历史轮次）
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// role=tool 时必填
	ToolCallID string `json:"tool_call_id,omitempty"`
	Name       string `json:"name,omitempty"`
}

// ChatRequest 聊天请求
type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	// Tools / ToolChoice 用于 OpenAI 风格的 tool calling（可选）
	Tools      []Tool      `json:"tools,omitempty"`
	ToolChoice interface{} `json:"tool_choice,omitempty"`
	Stream     bool        `json:"stream,omitempty"`
	// NoBrainInject 为 true 时不注入 boot-leader / brain 节选（演进决策等）。
	NoBrainInject bool `json:"-"`
	// BrainProfile 显式注入档位（worker minimal）；空则使用 brain.ActivePromptProfile()。
	BrainProfile brain.PromptProfile `json:"-"`
	// DisableThinking 为 true 时强制 DeepSeek thinking=disabled，保证 JSON 落在 content。
	DisableThinking bool `json:"-"`
	// LogKind 写入 llm.log 的 kind 字段（默认 chat_round）。
	LogKind string `json:"-"`
	// SubagentID / SessionID worker 轮次审计（可选）。
	SubagentID string `json:"-"`
	SessionID  string `json:"-"`
	// LogOutputCwd 本轮 LLM 请求日志应写入的产出区（~/.cata/llm/<sanitized>.log）；空则用全局。
	LogOutputCwd string `json:"-"`
}

// ChatResponse 聊天响应
type ChatResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int     `json:"index"`
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// IsAvailable 检查 LLM 客户端是否可用（检查 API key）
func IsAvailable() bool {
	return os.Getenv("LLM_API_KEY") != "" ||
		os.Getenv("OPENAI_API_KEY") != "" ||
		os.Getenv("DASHSCOPE_API_KEY") != "" ||
		os.Getenv("ANTHROPIC_API_KEY") != "" ||
		os.Getenv("DEEPSEEK_API_KEY") != ""
}

// APIFormatLabel 当前客户端协议格式。
func (c *Client) APIFormatLabel() string {
	if c == nil {
		return ""
	}
	return c.apiFormat
}

// ProviderLabel 展示用 provider 标签。
func (c *Client) ProviderLabel() string {
	if c == nil {
		return ""
	}
	return c.providerLabel
}

// APIURL 返回当前使用的 endpoint（可能已探测并记住）。
func (c *Client) APIURL() string {
	if c == nil {
		return ""
	}
	return c.apiURL
}

// APIKeyPresent 是否已配置 API key（只返回布尔，避免泄露密钥本身）。
func (c *Client) APIKeyPresent() bool {
	if c == nil {
		return false
	}
	return c.apiKey != ""
}

// TimeoutSeconds 返回客户端超时秒数。
func (c *Client) TimeoutSeconds() int {
	if c == nil {
		return 0
	}
	return int(c.timeout / time.Second)
}
