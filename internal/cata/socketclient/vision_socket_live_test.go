package socketclient

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
)

// TestLiveSocketInlineImage 真机端到端（走 socket 协议）：向已运行的 cata server
// 发一条带 base64 内联图片的 chat，模型应说出颜色。
//
// 需要外部环境（默认跳过）：
//
//	CATA_LIVE_SOCKET_TEST=1 CATA_LIVE_SOCKET_PATH=<socket> CATA_LIVE_SOCKET_CWD=<产出区> \
//	  CATA_HOME=<临时 home（含配好 LLM 的 config.json + 环境变量里的 key）> \
//	  go test ./internal/cata/socketclient/ -run TestLiveSocketInlineImage -v
func TestLiveSocketInlineImage(t *testing.T) {
	if os.Getenv("CATA_LIVE_SOCKET_TEST") != "1" {
		t.Skip("set CATA_LIVE_SOCKET_TEST=1 (plus PATH/CWD env) to run the live socket check")
	}
	socketPath := os.Getenv("CATA_LIVE_SOCKET_PATH")
	cwd := os.Getenv("CATA_LIVE_SOCKET_CWD")
	if socketPath == "" || cwd == "" {
		t.Fatal("CATA_LIVE_SOCKET_PATH / CATA_LIVE_SOCKET_CWD required")
	}

	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			if x < 32 {
				img.Set(x, y, color.RGBA{G: 255, A: 255}) // 左半绿
			} else {
				img.Set(x, y, color.RGBA{R: 255, G: 255, A: 255}) // 右半黄
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	b64 := base64.StdEncoding.EncodeToString(buf.Bytes())

	conn := NewConn(socketPath, cwd)
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var out strings.Builder
	h := &collectTokenHandler{onToken: func(s string) { out.WriteString(s) }}
	res, err := conn.ChatAsWithAttachments(ctx, "这张图左右两半分别是什么颜色？只回两个颜色词。", "",
		[]AttachmentReq{{Inline: &InlineAttachment{MIME: "image/png", Base64: b64}}}, h)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	t.Logf("socket reply=%q success=%v", res.Text, res.Success)
	low := strings.ToLower(res.Text)
	if !(strings.Contains(low, "绿") || strings.Contains(low, "green")) ||
		!(strings.Contains(low, "黄") || strings.Contains(low, "yellow")) {
		t.Fatalf("reply does not describe green/yellow halves: %q", res.Text)
	}
}

type collectTokenHandler struct{ onToken func(string) }

func (h *collectTokenHandler) OnToken(s string)   { h.onToken(s) }
func (h *collectTokenHandler) OnProgress(string)  {}
func (h *collectTokenHandler) OnToolStart(string) {}
func (h *collectTokenHandler) ConfirmExec(context.Context, ExecConfirmPrompt) (bool, error) {
	return false, nil
}
func (h *collectTokenHandler) Choose(context.Context, UserChoicePrompt) ([]string, error) {
	return nil, nil
}
