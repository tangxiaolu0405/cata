package llm

import (
	"fmt"
	"strings"

	"cata/internal/cata/config"
)

// 多模态能力（A 层：出站编码 + 模型能力路由）。
// 设计见 agents.md §多模态：history 存引用（MediaRef{id,mime}），出站再编码为 content[]；
// 按模型名声明 capabilities，缺省对未知模型保守视为仅 text。

// ModelCaps 单个模型的多模态能力（由 config.ModelCapCfg 归一化）。
type ModelCaps struct {
	Modalities          map[string]bool // "text" / "image"
	MaxImagesPerMessage int
	MaxImageBytes       int
	MaxRequestBytes     int
	ImageMIMEAllow      map[string]bool
}

// SupportsImage 是否支持图片输入。
func (c ModelCaps) SupportsImage() bool { return c.Modalities["image"] }

// SupportsAudio 是否支持音频输入。
func (c ModelCaps) SupportsAudio() bool { return c.Modalities["audio"] }

// SupportsText 是否支持文本（缺省 true）。
func (c ModelCaps) SupportsText() bool { return c.Modalities["text"] || len(c.Modalities) == 0 }

// SupportsModality 是否支持指定 modality（"image"/"audio"/"document"）。
// 未知/未配置的 modality 一律视为不支持（保守），文本缺省支持。
func (c ModelCaps) SupportsModality(mod string) bool {
	switch mod {
	case "text":
		return c.SupportsText()
	case "image", "audio", "document":
		return c.Modalities[mod]
	}
	return false
}

// DefaultImageMIMEs 默认允许的图片 MIME（与 DeepSeek 图像理解一致：JPEG/PNG/GIF/WebP）。
var DefaultImageMIMEs = []string{"image/png", "image/jpeg", "image/webp", "image/gif"}

// 附件上限默认值：对齐 DeepSeek 图像理解文档（api-docs.deepseek.com/zh-cn/guides/vision）：
//   - 单张图片（base64 / 外部 URL）≤ 32 MiB
//   - 单轮请求体 ≤ 48 MiB（base64 内联图片计入该上限，膨胀约 4/3）
//   - 单个请求最多 600 张图（本实现不做 600 张的硬校验，受请求体与上下文窗口自然约束）
const (
	// DefaultMaxImageBytes 单张图片**解码后**上限（32 MiB）。
	DefaultMaxImageBytes = 32 * 1024 * 1024
	// DefaultMaxRequestBytes 单轮请求体上限（48 MiB，base64 膨胀后计入）。
	DefaultMaxRequestBytes = 48 * 1024 * 1024
)

// capsForModel 从全局配置解析指定模型的能力（未知/未配置 → 仅 text）。
func capsForModel(cfg config.LLMConfig, model string) ModelCaps {
	mod, ok := cfg.Capabilities[model]
	if !ok {
		return ModelCaps{
			Modalities:      map[string]bool{"text": true},
			MaxImageBytes:   DefaultMaxImageBytes,
			MaxRequestBytes: DefaultMaxRequestBytes,
		}
	}
	c := ModelCaps{
		Modalities:          map[string]bool{},
		MaxImagesPerMessage: mod.MaxImagesPerMessage,
		MaxImageBytes:       mod.MaxImageBytes,
		MaxRequestBytes:     mod.MaxRequestBytes,
		ImageMIMEAllow:      map[string]bool{},
	}
	for _, m := range mod.Modalities {
		m = strings.ToLower(strings.TrimSpace(m))
		if m != "" {
			c.Modalities[m] = true
		}
	}
	if len(c.Modalities) == 0 {
		c.Modalities["text"] = true // 未声明 modalities 保守视为文本
	}
	if c.MaxImageBytes <= 0 {
		c.MaxImageBytes = DefaultMaxImageBytes
	}
	if c.MaxRequestBytes <= 0 {
		c.MaxRequestBytes = DefaultMaxRequestBytes
	}
	if len(mod.ImageMIMEAllow) == 0 {
		for _, m := range DefaultImageMIMEs {
			c.ImageMIMEAllow[m] = true
		}
	} else {
		for _, m := range mod.ImageMIMEAllow {
			c.ImageMIMEAllow[strings.ToLower(strings.TrimSpace(m))] = true
		}
	}
	return c
}

// ImageBytesLimitForCurrentModel 当前 chat 模型的单张图片上限（解码后字节数）。
// 供 server 侧 ingest 使用：附件读写在同一台机器/同一个模型配置下，两边上限保持一致
// （默认 32 MiB，可用 capabilities.<model>.max_image_bytes 覆盖）。
func ImageBytesLimitForCurrentModel() int {
	if config.Config == nil {
		return DefaultMaxImageBytes
	}
	return capsForModel(config.Config.LLM, currentChatModel(config.Config.LLM)).MaxImageBytes
}

// currentChatModel 解析当前实际用于对话的模型名（models["chat"] 优先，回退 model）。
func currentChatModel(cfg config.LLMConfig) string {
	if v, ok := cfg.Models["chat"]; ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return cfg.Model
}

// mimeAllowed 该模型是否允许此图片 MIME。
func (c ModelCaps) mimeAllowed(mime string) bool {
	if len(c.ImageMIMEAllow) == 0 {
		return true
	}
	return c.ImageMIMEAllow[strings.ToLower(strings.TrimSpace(mime))]
}

// audioMimeAllowed 模型是否允许此音频 MIME（默认白名单 DefaultAudioMIMEs）。
func (c ModelCaps) audioMimeAllowed(mime string) bool {
	m := strings.ToLower(strings.TrimSpace(mime))
	for _, allow := range DefaultAudioMIMEs {
		if m == allow {
			return true
		}
	}
	return false
}

// ImageModelRole 模型路由角色名（models["chat_vision"]）。
const ImageModelRole = "chat_vision"

// mediaModality 把媒体 MIME 归类为 modality（"image"/"audio"/"document"）。
func mediaModality(mime string) string {
	m := strings.ToLower(strings.TrimSpace(mime))
	switch {
	case strings.HasPrefix(m, "image/"):
		return "image"
	case strings.HasPrefix(m, "audio/"):
		return "audio"
	case m == "application/pdf":
		return "document"
	}
	return ""
}

// audioFormatFromMIME 从音频 MIME 推断 OpenAI input_audio 的 format。
func audioFormatFromMIME(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "audio/wav", "audio/x-wav":
		return "wav"
	case "audio/mpeg", "audio/mp3":
		return "mp3"
	case "audio/mp4", "audio/x-m4a":
		return "mp4"
	case "audio/ogg", "audio/opus":
		return "ogg"
	case "audio/flac":
		return "flac"
	case "audio/aac":
		return "aac"
	case "audio/webm":
		return "webm"
	}
	return "wav" // 未知音频默认 wav（OpenAI 兼容最通用的格式）
}

// DefaultAudioMIMEs 默认允许的音频 MIME（模型声明 audio modality 时启用）。
var DefaultAudioMIMEs = []string{"audio/wav", "audio/mpeg", "audio/mp3", "audio/mp4", "audio/aac", "audio/ogg", "audio/flac"}

// messagesHaveMedia 本轮是否存在带图的消息。
func messagesHaveMedia(msgs []Message) bool {
	for _, m := range msgs {
		if m.HasMedia() {
			return true
		}
	}
	return false
}

// messageModalities 返回本轮消息里出现的媒体 modality 集合。
func messageModalities(msgs []Message) map[string]bool {
	out := map[string]bool{}
	for _, m := range msgs {
		for _, ref := range m.Media {
			if mod := mediaModality(ref.MIME); mod != "" {
				out[mod] = true
			}
		}
	}
	return out
}

// resolveModelForMessages 按「附件 modality」路由模型：
//   - 无附件 → 当前 model（调用方已解析）
//   - 有附件 → 若当前 model 支持对应 modality 则保持不变；否则若 models["chat_vision"] 存在且支持则用它；
//     否则返回 error（不静默丢图）。
//
// 返回最终模型名；带图上文的会话用该模型。err 非 nil 表示当前无可用多模态模型。
func resolveModelForMessages(cfg config.LLMConfig, current string, msgs []Message) (result string, err error) {
	mods := messageModalities(msgs)
	// 带媒体但无法归类（文本/未知类型）→ 直接报错，不静默丢弃。
	for _, m := range msgs {
		for _, ref := range m.Media {
			if mediaModality(ref.MIME) == "" {
				return "", fmt.Errorf("附件 MIME %q 无法识别 modality（image/audio/document）", ref.MIME)
			}
		}
	}
	if len(mods) == 0 {
		return current, nil
	}
	// 校验附件 MIME / 数量（以将使用的模型能力为准）。
	if capsForModel(cfg, current).supportsAll(mods) {
		if e := validateMediaPayload(cfg, current, msgs); e != nil {
			return "", e
		}
		return current, nil
	}
	if v, ok := cfg.Models[ImageModelRole]; ok && strings.TrimSpace(v) != "" {
		v = strings.TrimSpace(v)
		if capsForModel(cfg, v).supportsAll(mods) {
			if e := validateMediaPayload(cfg, v, msgs); e != nil {
				return "", e
			}
			return v, nil
		}
	}
	return "", fmt.Errorf("请求含附件（%v），但模型 %q 与 vision 模型（llm.models.chat_vision）都缺少所需 modality", sortedModalities(mods), current)
}

// supportsAll 模型是否支持全部所需 modality。
func (c ModelCaps) supportsAll(mods map[string]bool) bool {
	for mod := range mods {
		if !c.SupportsModality(mod) {
			return false
		}
	}
	return true
}

func sortedModalities(mods map[string]bool) []string {
	out := make([]string, 0, len(mods))
	for _, m := range []string{"image", "audio", "document"} {
		if mods[m] {
			out = append(out, m)
		}
	}
	return out
}

// validateMediaPayload 校验数量 / 单张大小 / MIME 白名单 / 单轮请求体预算（以目标模型能力为准）。
// 限额默认对齐 DeepSeek：单张解码后 32 MiB、单轮请求体 48 MiB（base64 膨胀后计入）。
func validateMediaPayload(cfg config.LLMConfig, model string, msgs []Message) error {
	caps := capsForModel(cfg, model)
	nImg := 0
	payload := 0 // 本轮所有内联附件（base64 字符数）之和，近似请求体增量
	for _, m := range msgs {
		for _, ref := range m.Media {
			mod := mediaModality(ref.MIME)
			switch mod {
			case "image":
				nImg++
				if !caps.mimeAllowed(ref.MIME) {
					return fmt.Errorf("图片 MIME %q 不在模型 %q 白名单内", ref.MIME, model)
				}
			case "audio":
				if !caps.audioMimeAllowed(ref.MIME) {
					return fmt.Errorf("音频 MIME %q 不在模型 %q 白名单内", ref.MIME, model)
				}
			case "document":
				if ref.MIME != "application/pdf" {
					return fmt.Errorf("文档 MIME %q 不支持（仅 application/pdf）", ref.MIME)
				}
			default:
				return fmt.Errorf("附件 MIME %q 无法识别 modality（image/audio/document）", ref.MIME)
			}
			// 出站前 Data 才被填充；未填充（history 引用态）不参与体积校验。
			if ref.Data == "" {
				continue
			}
			payload += len(ref.Data)
			if dec := decodedBase64Len(ref.Data); dec > caps.MaxImageBytes {
				return fmt.Errorf("附件 %q 解码后 %s 超过模型 %q 单张上限 %s（capabilities.%s.max_image_bytes）",
					ref.ID, humanBytes(dec), model, humanBytes(caps.MaxImageBytes), model)
			}
		}
	}
	if caps.MaxImagesPerMessage > 0 && nImg > caps.MaxImagesPerMessage {
		return fmt.Errorf("单条消息图片数 %d 超过模型 %q 上限 %d", nImg, model, caps.MaxImagesPerMessage)
	}
	if payload > caps.MaxRequestBytes {
		return fmt.Errorf("本轮附件共 %s（base64 膨胀后）超过模型 %q 单轮请求体上限 %s：请减少张数或压缩图片（capabilities.%s.max_request_bytes）",
			humanBytes(payload), model, humanBytes(caps.MaxRequestBytes), model)
	}
	return nil
}

// decodedBase64Len base64 字符串解码后的字节数（不实际解码，按长度估算，含 padding 修正）。
func decodedBase64Len(s string) int {
	n := len(s)
	if n == 0 {
		return 0
	}
	pad := 0
	if strings.HasSuffix(s, "==") {
		pad = 2
	} else if strings.HasSuffix(s, "=") {
		pad = 1
	}
	return n/4*3 - pad
}

// humanBytes 人类可读的字节数（用于错误提示）。
func humanBytes(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	}
	return fmt.Sprintf("%d B", n)
}

// encodeContentForWire 把一条消息编码为 wire 的 content 值。
// text-only 消息 → string；带附件消息 → []contentPart（text + 按 modality 的 part）。
func encodeContentForWire(caps ModelCaps, m Message) (interface{}, error) {
	if !m.HasMedia() {
		return m.Content, nil
	}
	parts := []map[string]interface{}{}
	if strings.TrimSpace(m.Content) != "" {
		parts = append(parts, map[string]interface{}{"type": "text", "text": m.Content})
	}
	for _, ref := range m.Media {
		switch mediaModality(ref.MIME) {
		case "image":
			if !caps.SupportsImage() {
				return nil, fmt.Errorf("模型不支持 image，无法发送图片附件")
			}
			url := ref.URL
			if url == "" {
				if ref.Data == "" {
					return nil, fmt.Errorf("附件 %q 缺少 data/url（出站前未填充 base64）", ref.ID)
				}
				url = "data:" + ref.MIME + ";base64," + ref.Data
			}
			parts = append(parts, map[string]interface{}{
				"type":      "image_url",
				"image_url": map[string]interface{}{"url": url},
			})
		case "audio":
			if !caps.SupportsAudio() {
				return nil, fmt.Errorf("模型不支持 audio，无法发送音频附件")
			}
			if ref.Data == "" {
				return nil, fmt.Errorf("附件 %q 缺少 data（出站前未填充 base64）", ref.ID)
			}
			parts = append(parts, map[string]interface{}{
				"type": "input_audio",
				"input_audio": map[string]interface{}{
					"data":   ref.Data,
					"format": audioFormatFromMIME(ref.MIME),
				},
			})
		case "document":
			return nil, fmt.Errorf("文档附件（PDF）暂不支持出站编码：需先转图片或文本（M4 阶段按模型 capabilities 启用）")
		default:
			return nil, fmt.Errorf("附件 MIME %q 无法识别 modality", ref.MIME)
		}
	}
	return parts, nil
}
