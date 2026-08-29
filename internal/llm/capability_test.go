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
