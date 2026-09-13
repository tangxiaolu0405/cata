package server

import (
	"encoding/base64"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cata/internal/cata/config"
	"cata/internal/llm"
)

// writeTestPNG 生成一张 2x2 PNG。
func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestIngestAttachmentsOK(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "a.png")
	writeTestPNG(t, imgPath)

	media, _, rejects := ingestAttachments(dir, []AttachmentReq{{Path: "a.png"}})
	if len(rejects) != 0 {
		t.Fatalf("rejects=%v", rejects)
	}
	if len(media) != 1 {
		t.Fatalf("got %d", len(media))
	}
	if media[0].MIME != "image/png" || media[0].Data == "" {
		t.Fatalf("ref=%+v", media[0])
	}
}

func TestIngestAttachmentsInline(t *testing.T) {
	dir := t.TempDir()
	// 用一张真实 PNG 的 base64（2x2），验证 inline 分支全链路。
	f, err := os.CreateTemp(dir, "i*.png")
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	raw, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	media, _, rejects := ingestAttachments(dir, []AttachmentReq{{
		Inline: &InlineAttachment{MIME: "image/png", Base64: base64.StdEncoding.EncodeToString(raw)},
	}})
	if len(rejects) != 0 {
		t.Fatalf("rejects=%v", rejects)
	}
	if len(media) != 1 || media[0].MIME != "image/png" {
		t.Fatalf("media=%+v", media)
	}
}

func TestIngestAttachmentsRejectsNonImage(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(bad, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	media, _, rejects := ingestAttachments(dir, []AttachmentReq{{Path: "x.txt"}})
	if len(media) != 0 || len(rejects) != 1 {
		t.Fatalf("media=%d rejects=%d", len(media), len(rejects))
	}
	if !strings.Contains(rejects[0].Reason, "not allowed") {
		t.Fatalf("reason=%q", rejects[0].Reason)
	}
}

func TestIngestAttachmentsEscapesRejected(t *testing.T) {
	dir := t.TempDir()
	// 路径逃逸：相对路径 ../outside 应拒绝。
	media, _, rejects := ingestAttachments(dir, []AttachmentReq{{Path: "../outside.png"}})
	if len(media) != 0 || len(rejects) != 1 {
		t.Fatalf("media=%d rejects=%d", len(media), len(rejects))
	}
}

func TestIngestAttachmentsMixedPartial(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "a.png")
	writeTestPNG(t, imgPath)
	// 一合法 + 一非法：合法保留，非法进 rejects（不整体中断）。
	media, _, rejects := ingestAttachments(dir, []AttachmentReq{
		{Path: "a.png"},
		{Path: "x.txt"},
	})
	if len(media) != 1 || len(rejects) != 1 {
		t.Fatalf("media=%d rejects=%d", len(media), len(rejects))
	}
}

func TestSanitizeAttachmentsForMemory(t *testing.T) {
	media := []llmMediaRef{{ID: "a.png", MIME: "image/png", Data: "QUJD"}, {ID: "b.png", MIME: "image/png", Data: "QUJD"}}
	s := sanitizeAttachmentsForMemory(media)
	if !strings.Contains(s, "a.png") || !strings.Contains(s, "b.png") {
		t.Fatalf("summary=%q", s)
	}
	if strings.Contains(s, "QUJD") {
		t.Fatal("base64 should not leak into memory")
	}
}

// TestIngestPDFExtractsText 用假 pdftotext（在 PATH）验证 PDF 提取为文本附件，
// 且不进 media（不需要模型 document modality）。
func TestIngestPDFExtractsText(t *testing.T) {
	// 假 pdftotext：忽略输入/输出，向 stdout 打固定文本（模拟提取）。
	bin := filepath.Join(t.TempDir(), "pdftotext")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf 'hello pdf body'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))

	dir := t.TempDir()
	pdfPath := filepath.Join(dir, "doc.pdf")
	if err := os.WriteFile(pdfPath, []byte("%PDF-1.4 fake"), 0644); err != nil {
		t.Fatal(err)
	}

	media, docs, rejects := ingestAttachments(dir, []AttachmentReq{{Path: "doc.pdf"}})
	if len(rejects) != 0 {
		t.Fatalf("rejects=%v", rejects)
	}
	if len(media) != 0 {
		t.Fatalf("pdf must not become media: %v", media)
	}
	if len(docs) != 1 || docs[0].Name != "doc.pdf" || !strings.Contains(docs[0].Text, "hello pdf body") {
		t.Fatalf("docs=%+v", docs)
	}
}

// TestIngestPDFNoPdftotext 系统无 pdftotext 时，PDF 附件应被拒并提示安装 poppler-utils。
func TestIngestPDFNoPdftotext(t *testing.T) {
	// PATH 指向空目录，确保 LookPath("pdftotext") 失败。
	t.Setenv("PATH", t.TempDir())

	dir := t.TempDir()
	pdfPath := filepath.Join(dir, "doc.pdf")
	if err := os.WriteFile(pdfPath, []byte("%PDF-1.4 fake"), 0644); err != nil {
		t.Fatal(err)
	}

	media, docs, rejects := ingestAttachments(dir, []AttachmentReq{{Path: "doc.pdf"}})
	if len(media) != 0 || len(docs) != 0 || len(rejects) != 1 {
		t.Fatalf("media=%d docs=%d rejects=%d", len(media), len(docs), len(rejects))
	}
	if !strings.Contains(rejects[0].Reason, "pdftotext") {
		t.Fatalf("reason=%q, want pdftotext hint", rejects[0].Reason)
	}
}

// TestIngestInlineSniffsMIME 内联附件以**内容**判定 MIME（浏览器/剪贴板可能给错 MIME，
// DeepSeek 也是按实际内容判格式）：声明 octet-stream 的真 PNG 应被接受并纠正为 image/png。
func TestIngestInlineSniffsMIME(t *testing.T) {
	dir := t.TempDir()
	raw := testPNGBytes(t)

	media, _, rejects := ingestAttachments(dir, []AttachmentReq{{
		Inline: &InlineAttachment{MIME: "application/octet-stream", Base64: base64.StdEncoding.EncodeToString(raw)},
	}})
	if len(rejects) != 0 {
		t.Fatalf("rejects=%v", rejects)
	}
	if len(media) != 1 || media[0].MIME != "image/png" {
		t.Fatalf("media=%+v（应按内容纠正 MIME）", media)
	}
	// 反过来：声明 image/png 但内容不是图片 → 拒绝（不靠客户端声明放行）。
	media, _, rejects = ingestAttachments(dir, []AttachmentReq{{
		Inline: &InlineAttachment{MIME: "image/png", Base64: base64.StdEncoding.EncodeToString([]byte("not an image"))},
	}})
	if len(media) != 0 || len(rejects) != 1 {
		t.Fatalf("media=%v rejects=%v（非图片内容应被拒）", media, rejects)
	}
}

// TestIngestInlineSizeLimitFollowsModelCaps 单张上限跟模型 capabilities 走（默认 32 MiB）。
func TestIngestInlineSizeLimitFollowsModelCaps(t *testing.T) {
	prev := config.Config
	config.Config = &config.AppConfig{LLM: config.LLMConfig{
		Model: "m",
		Capabilities: map[string]config.ModelCapCfg{
			"m": {Modalities: []string{"text", "image"}, MaxImageBytes: 16}, // 故意压到 16 字节
		},
	}}
	t.Cleanup(func() { config.Config = prev })

	if got := maxAttachmentBytes(); got != 16 {
		t.Fatalf("maxAttachmentBytes()=%d want 16（应读模型 capabilities）", got)
	}
	raw := testPNGBytes(t)
	if int64(len(raw)) <= 16 {
		t.Fatalf("test png too small: %d", len(raw))
	}
	media, _, rejects := ingestAttachments(t.TempDir(), []AttachmentReq{{
		Inline: &InlineAttachment{MIME: "image/png", Base64: base64.StdEncoding.EncodeToString(raw)},
	}})
	if len(media) != 0 || len(rejects) != 1 || !strings.Contains(rejects[0].Reason, "exceeds max") {
		t.Fatalf("media=%v rejects=%v", media, rejects)
	}

	// 未配置模型时的兜底：默认 32 MiB（DeepSeek 文档单图上限）。
	config.Config = &config.AppConfig{}
	if got := maxAttachmentBytes(); got != int64(llm.DefaultMaxImageBytes) {
		t.Fatalf("fallback maxAttachmentBytes()=%d want %d", got, llm.DefaultMaxImageBytes)
	}
}

// testPNGBytes 生成一张 2x2 PNG 的字节。
func testPNGBytes(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "t.png")
	writeTestPNG(t, p)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
