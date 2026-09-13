package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"cata/internal/cata/config"
)

// TestLiveDeepSeekBase64Image 真机验证：用真实配置（~/.cata/config.json）向 DeepSeek
// 发一张 base64 内联图片，模型应能说出图中颜色。
//
// 默认跳过（需要网络 + API key + 会消耗 token）：
//
//	CATA_LIVE_VISION_TEST=1 go test ./internal/llm/ -run TestLiveDeepSeekBase64Image -v
func TestLiveDeepSeekBase64Image(t *testing.T) {
	if os.Getenv("CATA_LIVE_VISION_TEST") != "1" {
		t.Skip("set CATA_LIVE_VISION_TEST=1 to run the live DeepSeek vision check")
	}
	if _, err := config.LoadConfig(); err != nil {
		t.Fatalf("load config: %v", err)
	}
	if config.Config == nil {
		t.Fatal("no config loaded")
	}
	llmCfg := config.Config.LLM
	model := llmCfg.Model
	if v, ok := llmCfg.Models["chat"]; ok && strings.TrimSpace(v) != "" {
		model = strings.TrimSpace(v)
	}
	if capsForModel(llmCfg, model).SupportsImage() == false {
		t.Skipf("model %q has no image capability configured", model)
	}

	// 左半红、右半蓝的 64x64 PNG（模型能明确说出颜色）。
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			if x < 32 {
				img.Set(x, y, color.RGBA{R: 255, A: 255})
			} else {
				img.Set(x, y, color.RGBA{B: 255, A: 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	b64 := base64.StdEncoding.EncodeToString(buf.Bytes())
	t.Logf("inline image: %s, base64 %d chars", humanBytes(buf.Len()), len(b64))

	// 直接用配置里的连接定义（与 chat 出站路径一致：只差角色卡片注入）。
	client, err := NewClientFromLLMConfig(llmCfg, model)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	reply, err := client.Chat(ctx, []Message{{
		Role:    "user",
		Content: "这张图片左半部分和右半部分分别是什么颜色？只回答两个颜色词。",
		Media:   []MediaRef{{ID: "clip.png", MIME: "image/png", Data: b64}},
	}})
	if err != nil {
		t.Fatalf("chat with image: %v", err)
	}
	t.Logf("model=%s reply=%q", model, reply)
	low := strings.ToLower(reply)
	if !(strings.Contains(low, "红") || strings.Contains(low, "red")) ||
		!(strings.Contains(low, "蓝") || strings.Contains(low, "blue")) {
		t.Fatalf("reply does not describe red/blue halves: %q", reply)
	}
}
