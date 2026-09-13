package llm

import (
	"strings"
	"testing"

	"cata/internal/cata/config"
)

func testCfg() config.LLMConfig {
	return config.LLMConfig{
		Model: "text-model",
		Models: map[string]string{
			"default":     "text-model",
			"chat_vision": "vision-model",
		},
		Capabilities: map[string]config.ModelCapCfg{
			"text-model": {Modalities: []string{"text"}},
			"vision-model": {
				Modalities:          []string{"text", "image"},
				MaxImagesPerMessage: 4,
				MaxImageBytes:       1024 * 1024,
				ImageMIMEAllow:      []string{"image/png", "image/jpeg"},
			},
		},
	}
}

func msgsWithMedia(mime string, n int) []Message {
	var media []MediaRef
	for i := 0; i < n; i++ {
		media = append(media, MediaRef{ID: "img-x", MIME: mime, Data: "QUJD"})
	}
	return []Message{{Role: "user", Content: "看图", Media: media}}
}

func TestResolveModelForMessagesTextOnly(t *testing.T) {
	cfg := testCfg()
	m, err := resolveModelForMessages(cfg, "text-model", []Message{{Role: "user", Content: "hi"}})
	if err != nil || m != "text-model" {
		t.Fatalf("text-only: model=%q err=%v", m, err)
	}
}

func TestResolveModelForMessagesVisionFallback(t *testing.T) {
	cfg := testCfg()
	msgs := msgsWithMedia("image/png", 1)
	// 当前模型是 text 但配了 chat_vision → 路由到 vision。
	m, err := resolveModelForMessages(cfg, "text-model", msgs)
	if err != nil {
		t.Fatal(err)
	}
	if m != "vision-model" {
		t.Fatalf("model=%q want vision-model", m)
	}
}

func TestResolveModelForMessagesNoVision(t *testing.T) {
	cfg := config.LLMConfig{
		Model:        "text-model",
		Models:       map[string]string{"default": "text-model"},
		Capabilities: map[string]config.ModelCapCfg{"text-model": {Modalities: []string{"text"}}},
	}
	if _, err := resolveModelForMessages(cfg, "text-model", msgsWithMedia("image/png", 1)); err == nil {
		t.Fatal("expected error when no vision model configured")
	}
}

func TestResolveModelValidatesCountAndMIME(t *testing.T) {
	cfg := testCfg()
	// 超数量上限 → 报错。
	if _, err := resolveModelForMessages(cfg, "text-model", msgsWithMedia("image/png", 5)); err == nil {
		t.Fatal("expected error: too many images")
	}
	// MIME 不在白名单（vision 模型允许 png/jpeg，gif 拒绝）→ 报错。
	if _, err := resolveModelForMessages(cfg, "text-model", msgsWithMedia("image/gif", 1)); err == nil {
		t.Fatal("expected error: mime not allowed")
	}
}

// TestResolveModelForMessagesAudio 音频附件路由到声明 audio 的模型（chat_vision 兜底）。
func TestResolveModelForMessagesAudio(t *testing.T) {
	cfg := config.LLMConfig{
		Model:  "text-model",
		Models: map[string]string{"default": "text-model", "chat_vision": "audio-model"},
		Capabilities: map[string]config.ModelCapCfg{
			"text-model":  {Modalities: []string{"text"}},
			"audio-model": {Modalities: []string{"text", "audio"}},
		},
	}
	msgs := []Message{{Role: "user", Content: "听", Media: []MediaRef{{ID: "a.wav", MIME: "audio/wav", Data: "QUJD"}}}}
	m, err := resolveModelForMessages(cfg, "text-model", msgs)
	if err != nil {
		t.Fatal(err)
	}
	if m != "audio-model" {
		t.Fatalf("model=%q want audio-model", m)
	}
	// 音频 MIME 不在默认白名单（audio/gif 无此类型；用 text/x-audio）→ 报错。
	bad := []Message{{Role: "user", Content: "听", Media: []MediaRef{{ID: "a.bin", MIME: "application/octet-stream", Data: "QUJD"}}}}
	if _, err := resolveModelForMessages(cfg, "text-model", bad); err == nil {
		t.Fatal("expected error for unclassifiable mime")
	}
}

// TestEncodeContentForWireAudio 验证 audio 出站编码为 input_audio part。
func TestEncodeContentForWireAudio(t *testing.T) {
	caps := capsForModel(config.LLMConfig{
		Model: "audio-model",
		Capabilities: map[string]config.ModelCapCfg{
			"audio-model": {Modalities: []string{"text", "audio"}},
		},
	}, "audio-model")
	m := Message{Role: "user", Content: "听", Media: []MediaRef{{ID: "a", MIME: "audio/wav", Data: "QUJD"}}}
	v, err := encodeContentForWire(caps, m)
	if err != nil {
		t.Fatal(err)
	}
	parts, ok := v.([]map[string]interface{})
	if !ok || len(parts) != 2 {
		t.Fatalf("parts=%v", v)
	}
	if parts[1]["type"] != "input_audio" {
		t.Fatalf("part=%v", parts[1])
	}
	ia := parts[1]["input_audio"].(map[string]interface{})
	if ia["data"] != "QUJD" || ia["format"] != "wav" {
		t.Fatalf("input_audio=%v", ia)
	}
	// 纯文本模型遇音频应报错（不静默丢）。
	textCaps := capsForModel(config.LLMConfig{
		Model: "text-model",
		Capabilities: map[string]config.ModelCapCfg{
			"text-model": {Modalities: []string{"text"}},
		},
	}, "text-model")
	if _, err := encodeContentForWire(textCaps, m); err == nil {
		t.Fatal("text model with audio should error")
	}
}

// TestMediaModalityAndAudioFormat 分类与格式推断的辅助表。
func TestMediaModalityAndAudioFormat(t *testing.T) {
	cases := []struct {
		mime, wantMod, wantFmt string
	}{
		{"image/png", "image", ""},
		{"audio/wav", "audio", "wav"},
		{"audio/mpeg", "audio", "mp3"},
		{"application/pdf", "document", ""},
		{"text/plain", "", ""},
	}
	for _, c := range cases {
		if got := mediaModality(c.mime); got != c.wantMod {
			t.Fatalf("mediaModality(%q)=%q want %q", c.mime, got, c.wantMod)
		}
		if c.wantMod != "audio" {
			continue
		}
		if got := audioFormatFromMIME(c.mime); got != c.wantFmt {
			t.Fatalf("audioFormatFromMIME(%q)=%q want %q", c.mime, got, c.wantFmt)
		}
	}
}

// TestEncodeContentForWire 验证 vision 出站编码为 content[]（text + image_url data URL）。
func TestEncodeContentForWire(t *testing.T) {
	caps := capsForModel(testCfg(), "vision-model")
	m := Message{Role: "user", Content: "看图", Media: []MediaRef{{ID: "a", MIME: "image/png", Data: "QUJD"}}}
	v, err := encodeContentForWire(caps, m)
	if err != nil {
		t.Fatal(err)
	}
	parts, ok := v.([]map[string]interface{})
	if !ok || len(parts) != 2 {
		t.Fatalf("parts=%v", v)
	}
	if parts[0]["type"] != "text" || parts[0]["text"] != "看图" {
		t.Fatalf("text part=%v", parts[0])
	}
	img := parts[1]["image_url"].(map[string]interface{})
	if !strings.HasPrefix(img["url"].(string), "data:image/png;base64,") {
		t.Fatalf("image_url=%v", img)
	}
}

// TestEncodeContentForWireTextModel 文本模型遇图应报错（不静默丢图）。
func TestEncodeContentForWireTextModel(t *testing.T) {
	caps := capsForModel(testCfg(), "text-model")
	m := msgsWithMedia("image/png", 1)[0]
	if _, err := encodeContentForWire(caps, m); err == nil {
		t.Fatal("text model with image should error")
	}
}

// TestCapsDefaultsMatchDeepSeek 附件限额默认值对齐 DeepSeek 图像理解文档：
// 单张解码后 32 MiB、单轮请求体 48 MiB；显式配置优先。
func TestCapsDefaultsMatchDeepSeek(t *testing.T) {
	if DefaultMaxImageBytes != 32*1024*1024 {
		t.Fatalf("DefaultMaxImageBytes=%d want 32MiB", DefaultMaxImageBytes)
	}
	if DefaultMaxRequestBytes != 48*1024*1024 {
		t.Fatalf("DefaultMaxRequestBytes=%d want 48MiB", DefaultMaxRequestBytes)
	}
	if DefaultImageTokenEstimate != 1024 {
		t.Fatalf("DefaultImageTokenEstimate=%d want 1024", DefaultImageTokenEstimate)
	}
	// 未配置 capabilities 的未知模型也拿到默认限额（不是 0，避免「无上限」）。
	caps := capsForModel(config.LLMConfig{Model: "unknown"}, "unknown")
	if caps.MaxImageBytes != DefaultMaxImageBytes || caps.MaxRequestBytes != DefaultMaxRequestBytes {
		t.Fatalf("unknown model caps=%+v", caps)
	}
	// 显式配置优先。
	cfg := config.LLMConfig{
		Model: "m",
		Capabilities: map[string]config.ModelCapCfg{
			"m": {Modalities: []string{"text", "image"}, MaxImageBytes: 1234, MaxRequestBytes: 5678},
		},
	}
	caps = capsForModel(cfg, "m")
	if caps.MaxImageBytes != 1234 || caps.MaxRequestBytes != 5678 {
		t.Fatalf("explicit caps ignored: %+v", caps)
	}
}

// TestValidateMediaPayloadLimits 单张上限与单轮请求体预算（base64 膨胀后）都要拦住。
func TestValidateMediaPayloadLimits(t *testing.T) {
	cfg := config.LLMConfig{
		Model: "vision",
		Capabilities: map[string]config.ModelCapCfg{
			"vision": {Modalities: []string{"text", "image"}, MaxImageBytes: 3000, MaxRequestBytes: 4000},
		},
	}
	// base64 长度 4000 → 解码 3000 字节：正好等于单张上限（放行）。
	ok := []Message{{Role: "user", Media: []MediaRef{{ID: "a.png", MIME: "image/png", Data: strings.Repeat("A", 4000)}}}}
	if err := validateMediaPayload(cfg, "vision", ok); err != nil {
		t.Fatalf("at-limit image rejected: %v", err)
	}
	// 解码 3003 字节 > 3000：单张超限。
	over := []Message{{Role: "user", Media: []MediaRef{{ID: "big.png", MIME: "image/png", Data: strings.Repeat("A", 4004)}}}}
	err := validateMediaPayload(cfg, "vision", over)
	if err == nil || !strings.Contains(err.Error(), "单张上限") {
		t.Fatalf("expected 单张上限 error, got %v", err)
	}
	// 两张各 2000 字节（base64 2668）→ 合计 5336 > 4000：请求体超预算。
	two := []Message{{Role: "user", Media: []MediaRef{
		{ID: "a.png", MIME: "image/png", Data: strings.Repeat("A", 2668)},
		{ID: "b.png", MIME: "image/png", Data: strings.Repeat("A", 2668)},
	}}}
	err = validateMediaPayload(cfg, "vision", two)
	if err == nil || !strings.Contains(err.Error(), "请求体上限") {
		t.Fatalf("expected 请求体上限 error, got %v", err)
	}
}

// TestDecodedBase64Len 长度换算（含 padding）。
func TestDecodedBase64Len(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"QUJD", 3}, // "ABC"
		{"QQ==", 1}, // "A"
		{"QUI=", 2}, // "AB"
		{"AAAA", 3},
	}
	for _, c := range cases {
		if got := decodedBase64Len(c.in); got != c.want {
			t.Fatalf("decodedBase64Len(%q)=%d want %d", c.in, got, c.want)
		}
	}
}
