package server

import (
	"encoding/base64"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	media, rejects := ingestAttachments(dir, []AttachmentReq{{Path: "a.png"}})
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
	media, rejects := ingestAttachments(dir, []AttachmentReq{{
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
	media, rejects := ingestAttachments(dir, []AttachmentReq{{Path: "x.txt"}})
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
	media, rejects := ingestAttachments(dir, []AttachmentReq{{Path: "../outside.png"}})
	if len(media) != 0 || len(rejects) != 1 {
		t.Fatalf("media=%d rejects=%d", len(media), len(rejects))
	}
}

func TestIngestAttachmentsMixedPartial(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "a.png")
	writeTestPNG(t, imgPath)
	// 一合法 + 一非法：合法保留，非法进 rejects（不整体中断）。
	media, rejects := ingestAttachments(dir, []AttachmentReq{
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
