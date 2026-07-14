package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"cata/internal/cata/brain"
	"cata/internal/cata/clock"
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
	maxBrainExcerptBytesTotal  = 20000
	// boot-leader 正文码点上限（单文件过大时截断）
	maxBootLeaderRunes     = 10000
	maxBootLeaderRunesTask = 5000

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
	apiKey       string
	apiURL       string
	model        string
	providerLabel string // 展示标签，不参与协议选择
	apiFormat    string
	maxTokens    int
	timeout            time.Duration
	httpClient         *http.Client
	streamHTTPClient   *http.Client
	adapter            APIAdapter
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
				log.Printf("WARNING: SOCKS5 proxy detected (%s) but not fully supported. If you see EOF errors, try: 1) Start your proxy server, 2) Use HTTP proxy instead, or 3) Unset proxy env vars", proxyURL)
			}
		}
	}
	return tr
}

func newHTTPClientPair(timeout time.Duration) (*http.Client, *http.Client) {
	tr := configureHTTPTransport(timeout)
	regular := &http.Client{Timeout: timeout, Transport: tr}
	stream := &http.Client{Timeout: streamRoundTimeout(timeout), Transport: tr}
	return regular, stream
}

var (
	bootLeaderOnce   sync.Once
	bootLeaderPrompt string
)

// loadBootLeaderPrompt 只读一次 brain/boot-leader.md，用作所有对话的通用系统提示词前缀。
func loadBootLeaderPrompt() string {
	bootLeaderOnce.Do(func() {
		path := brain.BootLeaderPath()
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("Warning: failed to read boot-leader.md from %s: %v", path, err)
			return
		}
		s := brain.CompactExcessiveNewlines(strings.TrimSpace(string(data)))
		bootLeaderPrompt = truncateRunes(s, maxBootLeaderRunes)
	})
	return bootLeaderPrompt
}

func effectiveBootLeaderPrompt() string {
	return effectiveBootLeaderPromptFor(brain.ActivePromptProfile())
}

func effectiveBootLeaderPromptFor(profile brain.PromptProfile) string {
	switch brain.ProfileRank(profile) {
	case 0:
		return brain.LoadMinimalBootPrompt()
	case 1:
		prompt := strings.TrimSpace(loadBootLeaderPrompt())
		if prompt == "" {
			return brain.LoadMinimalBootPrompt()
		}
		return truncateRunes(prompt, maxBootLeaderRunesTask)
	default:
		return strings.TrimSpace(loadBootLeaderPrompt())
	}
}

// withBootLeaderSystemMessage 确保每次请求的消息列表前面都有 boot-leader.md 作为系统提示词。
func withBootLeaderSystemMessage(messages []Message) []Message {
	return withBootLeaderSystemMessageFor(messages, brain.ActivePromptProfile())
}

func withBootLeaderSystemMessageFor(messages []Message, profile brain.PromptProfile) []Message {
	prompt := effectiveBootLeaderPromptFor(profile)
	if prompt == "" {
		return ensureCataBrainExcerptSystemFor(messages, profile)
	}

	if len(messages) > 0 && messages[0].Role == "system" && strings.TrimSpace(messages[0].Content) == prompt {
		return ensureCataBrainExcerptSystemFor(messages, profile)
	}

	out := make([]Message, 0, len(messages)+1)
	out = append(out, Message{Role: "system", Content: prompt})
	out = append(out, messages...)
	return ensureCataBrainExcerptSystemFor(out, profile)
}

// ensureCataBrainExcerptSystem 在 boot-leader 之后插入路径块 + 脑子节选（若尚未存在）。
func ensureCataBrainExcerptSystem(msgs []Message) []Message {
	return ensureCataBrainExcerptSystemFor(msgs, brain.ActivePromptProfile())
}

func ensureCataBrainExcerptSystemFor(msgs []Message, profile brain.PromptProfile) []Message {
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
	ext := brain.TerminalBrainSystemExtensionFor(profile, perFile, total)
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
	if config.Config != nil && config.Config.LLM.Enabled {
		llmCfg := config.Config.LLM
		model := resolveModelForRole(llmCfg, role)
		return NewClientFromLLMConfig(llmCfg, model)
	}

	// 配置未启用或未加载，沿用现有环境变量与默认逻辑
	return NewClient()
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
	apiURL = NormalizeAPIURL(apiFormat, apiURL)

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

	log.Printf("Creating LLM Client: label=%s format=%s URL=%s Model=%s APIKey present=%v timeout=%s stream_timeout=%s",
		providerLabel, apiFormat, apiURL, model, apiKey != "", timeout, streamRoundTimeout(timeout))

	return &Client{
		apiKey:           apiKey,
		apiURL:           apiURL,
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
	return &Client{
		apiKey:           apiKey,
		apiURL:           NormalizeAPIURL(format, apiURL),
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

// Summarize 生成摘要
func (c *Client) Summarize(content string, instructions string) (string, error) {
	systemPrompt := "你是一个专业的文本摘要助手。请根据用户提供的内容生成简洁、准确的摘要。"
	if instructions != "" {
		systemPrompt = instructions
	}

	userPrompt := fmt.Sprintf("请为以下内容生成摘要：\n\n%s", content)

	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	req := ChatRequest{
		Model:    c.model,
		Messages: messages,
		MaxTokens: c.maxTokens,
		Temperature: 0.3, // 较低温度以获得更一致的摘要
	}

	resp, _, err := c.chat(req, nil, "", false)
	if err != nil {
		return "", fmt.Errorf("failed to generate summary: %w", err)
	}

	if resp.Error != nil {
		return "", fmt.Errorf("API error: %s (type: %s, code: %s)", 
			resp.Error.Message, resp.Error.Type, resp.Error.Code)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no response from LLM")
	}

	return resp.Choices[0].Message.Content, nil
}

// PreprocessQuery 预处理查询：将自然语言转换为检索关键词和类别
type QueryPreprocessResult struct {
	Keywords []string `json:"keywords"` // 检索关键词列表
	Category string   `json:"category"` // 类别：preference、fact、logic
	Domain   string   `json:"domain"`   // 领域：dev、learning、life
	Intent   string   `json:"intent"`  // 检索意图描述
}

// PreprocessQuery 预处理查询：自然语言 → 检索意图 + 关键词 + category
func (c *Client) PreprocessQuery(query string) (*QueryPreprocessResult, error) {
	systemPrompt := `你是一个查询预处理助手。请分析用户的自然语言查询，提取检索关键词、类别和领域。

类别（category）：
- preference: 偏好、习惯、目标、身份认同相关
- fact: 事实、事件、记录相关
- logic: 逻辑、推理、设计、架构相关

领域（domain）：
- dev: 开发、技术、项目相关
- learning: 学习、笔记、知识相关
- life: 生活、健康、习惯相关

请以 JSON 格式返回结果，包含 keywords（关键词数组）、category、domain 和 intent（检索意图描述）。`

	userPrompt := fmt.Sprintf("请分析以下查询并提取检索信息：\n\n%s", query)

	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	req := ChatRequest{
		Model:    c.model,
		Messages: messages,
		MaxTokens: 500,
		Temperature: 0.3,
	}

	resp, _, err := c.chat(req, nil, "", false)
	if err != nil {
		return nil, fmt.Errorf("failed to preprocess query: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("API error: %s (type: %s, code: %s)", 
			resp.Error.Message, resp.Error.Type, resp.Error.Code)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no response from LLM")
	}

	// 解析 JSON 响应
	result := &QueryPreprocessResult{}
	responseText := resp.Choices[0].Message.Content

	// 尝试提取 JSON（可能包含 markdown 代码块）
	jsonStart := strings.Index(responseText, "{")
	jsonEnd := strings.LastIndex(responseText, "}")
	if jsonStart >= 0 && jsonEnd > jsonStart {
		responseText = responseText[jsonStart : jsonEnd+1]
	}

	if err := json.Unmarshal([]byte(responseText), result); err != nil {
		// 如果解析失败，尝试简单提取关键词
		return c.fallbackPreprocess(query), nil
	}

	// 验证和清理结果
	if len(result.Keywords) == 0 {
		// 如果没有关键词，使用原始查询
		result.Keywords = []string{query}
	}
	if result.Category == "" {
		result.Category = "fact" // 默认类别
	}
	if result.Domain == "" {
		result.Domain = "" // 空字符串表示不过滤
	}

	return result, nil
}

// fallbackPreprocess 简单的回退预处理（当 LLM 不可用或解析失败时）
func (c *Client) fallbackPreprocess(query string) *QueryPreprocessResult {
	keywords := strings.Fields(query)
	category := "fact"
	domain := ""

	queryLower := strings.ToLower(query)
	
	// 简单的类别判断
	if strings.Contains(queryLower, "偏好") || strings.Contains(queryLower, "习惯") || 
	   strings.Contains(queryLower, "目标") || strings.Contains(queryLower, "身份") {
		category = "preference"
	} else if strings.Contains(queryLower, "项目") || strings.Contains(queryLower, "设计") || 
	          strings.Contains(queryLower, "架构") || strings.Contains(queryLower, "逻辑") {
		category = "logic"
	}

	// 简单的领域判断
	if strings.Contains(queryLower, "开发") || strings.Contains(queryLower, "项目") || 
	   strings.Contains(queryLower, "技术") || strings.Contains(queryLower, "代码") {
		domain = "dev"
	} else if strings.Contains(queryLower, "学习") || strings.Contains(queryLower, "笔记") || 
	          strings.Contains(queryLower, "知识") {
		domain = "learning"
	} else if strings.Contains(queryLower, "生活") || strings.Contains(queryLower, "健康") || 
	          strings.Contains(queryLower, "习惯") {
		domain = "life"
	}

	return &QueryPreprocessResult{
		Keywords: keywords,
		Category: category,
		Domain:   domain,
		Intent:   fmt.Sprintf("检索与 '%s' 相关的内容", query),
	}
}

// Chat 发送聊天请求
func (c *Client) Chat(messages []Message) (string, error) {
	req := ChatRequest{
		Model:      c.model,
		Messages:   messages,
		MaxTokens:  c.maxTokens,
		Temperature: 0.7,
	}

	resp, _, err := c.chat(req, nil, "", false)
	if err != nil {
		return "", fmt.Errorf("failed to chat: %w", err)
	}

	if resp.Error != nil {
		return "", fmt.Errorf("API error: %s (type: %s, code: %s)", 
			resp.Error.Message, resp.Error.Type, resp.Error.Code)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no response from LLM")
	}

	return resp.Choices[0].Message.Content, nil
}

// ChatEvolution 演进决策：低温度、不写 llm.log（skipAppendLog）。
// maxTokens=0 时不设 API max_tokens（由模型默认输出上限）；>0 则限制输出长度。
// 不注入 boot-leader / brain 节选；禁用 thinking，避免 JSON 落在 reasoning_content。
func (c *Client) ChatEvolution(messages []Message, maxTokens int) (string, error) {
	req := ChatRequest{
		Model:           c.model,
		Messages:        messages,
		MaxTokens:       maxTokens,
		Temperature:     0.2,
		NoBrainInject:   true,
		DisableThinking: true,
	}
	resp, _, err := c.chat(req, nil, "", true)
	if err != nil {
		return "", fmt.Errorf("evolution chat: %w", err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("API error: %s", resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no response from LLM")
	}
	msg := resp.Choices[0].Message
	return assistantText(msg.Content, msg.ReasoningContent), nil
}

// ChatWithTools 调用带有 tools 列表的对话接口，返回助手回复和工具调用列表。
// maxTokens / temperature 传 0 则回退到客户端默认值。
func (c *Client) ChatWithTools(messages []Message, tools []Tool, toolChoice string, maxTokens int, temperature float64) (string, []ToolCall, error) {
	if maxTokens <= 0 {
		maxTokens = c.maxTokens
	}
	if temperature == 0 {
		temperature = 0.7
	}

	req := ChatRequest{
		Model:       c.model,
		Messages:    messages,
		MaxTokens:   maxTokens,
		Temperature: temperature,
	}

	resp, toolCalls, err := c.chat(req, tools, toolChoice, false)
	if err != nil {
		return "", nil, fmt.Errorf("failed to chat with tools: %w", err)
	}

	if resp.Error != nil {
		return "", nil, fmt.Errorf("API error: %s (type: %s, code: %s)", 
			resp.Error.Message, resp.Error.Type, resp.Error.Code)
	}

	if len(resp.Choices) == 0 {
		return "", nil, fmt.Errorf("no response from LLM")
	}

	return resp.Choices[0].Message.Content, toolCalls, nil
}

// buildHTTPChatRequest 构建 HTTP 请求（stream 为 true 时使用 SSE）。
// injectBrain 由 req.NoBrainInject 控制；演进模块应设 NoBrainInject=true。
func (c *Client) buildHTTPChatRequest(ctx context.Context, req ChatRequest, tools []Tool, toolChoice string, stream bool) (*http.Request, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("API key is empty")
	}
	msgs := req.Messages
	if !req.NoBrainInject {
		profile := req.BrainProfile
		if profile == "" {
			profile = brain.ActivePromptProfile()
		}
		msgs = SanitizeMessagesToolCalls(compactMessageContentForAPI(withBootLeaderSystemMessageFor(req.Messages, profile)))
	} else {
		msgs = SanitizeMessagesToolCalls(compactMessageContentForAPI(req.Messages))
	}
	req.Messages = msgs
	log.Printf("LLM Request: URL=%s, Model=%s, format=%s label=%s adapter=%T, stream=%v, APIKey present=%v",
		c.apiURL, c.model, c.apiFormat, c.providerLabel, c.adapter, stream, c.apiKey != "")

	httpReq, err := c.adapter.BuildRequest(c.apiURL, c.apiKey, c.model, req.Messages, req.MaxTokens, req.Temperature, tools, toolChoice, stream, req.DisableThinking)
	if err != nil {
		return nil, err
	}
	if ctx != nil {
		httpReq = httpReq.WithContext(ctx)
	}
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	return httpReq, nil
}

// chat 发送 HTTP 请求到 LLM API（内部统一入口）。skipAppendLog 为 true 时不写 llm.log（由调用方统一写）。
func (c *Client) chat(req ChatRequest, tools []Tool, toolChoice string, skipAppendLog bool) (*ChatResponse, []ToolCall, error) {
	httpReq, err := c.buildHTTPChatRequest(context.Background(), req, tools, toolChoice, false)
	if err != nil {
		return nil, nil, err
	}

	// 调试：检查 header 是否设置（仅在开发时启用）
	if os.Getenv("DEBUG_LLM") == "true" {
		log.Printf("DEBUG: API URL: %s", c.apiURL)
		log.Printf("DEBUG: Provider adapter: %T format=%s", c.adapter, c.apiFormat)
		log.Printf("DEBUG: API Key length: %d", len(c.apiKey))
		authHeader := httpReq.Header.Get("Authorization")
		if len(authHeader) > 20 {
			log.Printf("DEBUG: Authorization header: %s...", authHeader[:20])
		} else {
			log.Printf("DEBUG: Authorization header: %s", authHeader)
		}
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// EOF 错误可能是连接问题或 API key 问题
		log.Printf("HTTP Request failed: URL=%s, Error=%v", c.apiURL, err)
		errStr := err.Error()
		if errStr == "EOF" || strings.Contains(errStr, "EOF") {
			authHeader := httpReq.Header.Get("Authorization")
			authPresent := authHeader != ""
			authPrefix := ""
			if len(authHeader) > 20 {
				authPrefix = authHeader[:20]
			} else {
				authPrefix = authHeader
			}
			
			// 针对千问的 EOF 错误提供更具体的建议
			helpMsg := ""
			if strings.Contains(c.apiURL, "dashscope") {
				helpMsg = "\nFor Qwen/DashScope EOF errors, try:\n" +
					"1. Verify API key matches your region (China/International/US)\n" +
					"2. Try international endpoint: https://dashscope-intl.aliyuncs.com/compatible-mode/v1/chat/completions\n" +
					"3. Verify API key is valid and has proper permissions\n" +
					"4. Check network connectivity to DashScope servers"
			}
			
			return nil, nil, fmt.Errorf("connection closed unexpectedly (EOF). URL=%s, AuthHeader present=%v, AuthPrefix=%s.%s\nPossible causes: 1) API key invalid/missing or region mismatch, 2) Network issue, 3) API endpoint incorrect", 
				c.apiURL, authPresent, authPrefix, helpMsg)
		}
		return nil, nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read response body: %v", err)
		return nil, nil, fmt.Errorf("failed to read response: %w", err)
	}

	// 检查 HTTP 状态码
	if resp.StatusCode != http.StatusOK {
		errorMsg := string(body)
		if len(errorMsg) > 500 {
			errorMsg = errorMsg[:500] + "..."
		}
		log.Printf("API returned non-200 status: %d, URL=%s, Body: %s", resp.StatusCode, c.apiURL, errorMsg)
		
		// 对于 404 错误，提供更具体的提示
		if resp.StatusCode == http.StatusNotFound {
			return nil, nil, fmt.Errorf("API endpoint not found (404). URL=%s. Please check: 1) URL is correct (should end with /chat/completions), 2) Model name is valid (e.g., qwen-plus, qwen-turbo), 3) API endpoint matches your region", c.apiURL)
		}
		
		return nil, nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, errorMsg)
	}

	// 使用 provider 解析响应（得到文本与 tools 调用）
	content, toolCalls, err := c.adapter.ParseResponse(body)
	if err != nil {
		return nil, nil, err
	}

	// 将本次 LLM 交互写入可选的日志文件（通过 LLM_LOG_FILE 控制，避免影响正常 stdout 日志）。
	if !skipAppendLog {
		c.appendLLMLog(req, tools, toolChoice, content, toolCalls, body)
	}

	// 转换为 ChatResponse 格式（向后兼容）
	chatResp := &ChatResponse{
		Choices: []struct {
			Index        int     `json:"index"`
			Message      Message `json:"message"`
			FinishReason string  `json:"finish_reason"`
		}{
			{
				Index: 0,
				Message: Message{
					Role:    "assistant",
					Content: content,
				},
				FinishReason: "stop",
			},
		},
	}

	return chatResp, toolCalls, nil
}

// assistantText 合并 content 与 reasoning_content（DeepSeek thinking 可能只填后者）。
func assistantText(content, reasoning string) string {
	content = strings.TrimSpace(content)
	reasoning = strings.TrimSpace(reasoning)
	if content != "" {
		return content
	}
	return reasoning
}

// inferPromptSources 标注本条请求里各段 system，便于阅读 llm.log。
func inferPromptSources(msgs []Message) []string {
	var out []string
	boot := strings.TrimSpace(loadBootLeaderPrompt())
	i := 0
	for i < len(msgs) && msgs[i].Role == "system" {
		c := strings.TrimSpace(msgs[i].Content)
		switch {
		case boot != "" && c == boot:
			out = append(out, "brain/boot-leader.md")
		case strings.HasPrefix(c, brain.TerminalBundleSystemPrefix):
			out = append(out, "brain/core+workflow+hot (server excerpt)")
		default:
			out = append(out, "system:other")
		}
		i++
	}
	return out
}

// appendLLMLog 将一轮 LLM 对话追加为一行 JSON（JSON Lines）。
// 默认写入 prompt 组件清单（static 仅 chars/preview，conversation 仅末尾几条全文），避免每轮重复刷 boot-leader 全文。
// 设置 LLM_LOG_VERBOSE=1 可恢复完整 messages/tools/raw_body。
// 日志路径由环境变量 LLM_LOG_FILE 控制，默认 llm.log。
func (c *Client) appendLLMLog(req ChatRequest, tools []Tool, toolChoice string, content string, toolCalls []ToolCall, rawBody []byte) {
	logPath := brain.LLMLogPath()

	msgsCopy := append([]Message(nil), req.Messages...)
	effectiveMessages := withBootLeaderSystemMessage(msgsCopy)

	respLog := map[string]interface{}{
		"content": content,
	}
	if len(toolCalls) > 0 {
		respLog["tool_calls"] = toolCalls
	}

	entry := map[string]interface{}{
		"timestamp":      clock.RFC3339(),
		"kind":           llmLogKind(req),
		"url":            c.apiURL,
		"model":          c.model,
		"prompt_sources": inferPromptSources(effectiveMessages),
		"prompt":         buildPromptManifest(effectiveMessages, tools, toolChoice, req.MaxTokens, req.Temperature),
		"response":       respLog,
	}
	if req.SubagentID != "" {
		entry["subagent_id"] = req.SubagentID
	}
	if req.SessionID != "" {
		entry["session_id"] = req.SessionID
	}
	if llmLogVerbose() {
		entry["request_full"] = map[string]interface{}{
			"messages":    cloneMessagesForLLMLog(effectiveMessages),
			"tools":       tools,
			"tool_choice": toolChoice,
		}
		if len(rawBody) > 0 {
			entry["raw_body"] = string(rawBody)
		}
	}

	data, err := json.Marshal(entry)
	if err != nil {
		log.Printf("Failed to marshal LLM log entry: %v", err)
		return
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to open LLM log file %s: %v", logPath, err)
		return
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		log.Printf("Failed to write LLM log entry to %s: %v", logPath, err)
	}
}

func llmLogKind(req ChatRequest) string {
	if k := strings.TrimSpace(req.LogKind); k != "" {
		return k
	}
	return "chat_round"
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
