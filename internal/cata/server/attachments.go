package server

import (
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
)

// AttachmentReject 被拒绝的附件（reason + 原始 path/id）。
type AttachmentReject struct {
	Path   string `json:"path,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// ingestAttachments 把客户端随 chat 发送的附件读取为带 base64 的 MediaRef。
// 两种来源：path（相对产出区 / attachment_dir 白名单的本地文件）与 inline（剪贴板粘贴等已编码内容）。
// 校验：文件存在且为常规文件、大小 ≤ 10MiB、MIME 白名单（png/jpeg/webp/gif）。
// 逐条失败不中断整体：被拒项进 rejects（调用方发 attachment_rejected 事件），合法项继续。
func ingestAttachments(outCwd string, reqs []AttachmentReq) (media []llmMediaRef, rejects []AttachmentReject) {
	for _, r := range reqs {
		if r.Path != "" {
			ref, reason := ingestAttachmentPath(outCwd, strings.TrimSpace(r.Path))
			if reason != "" {
				rejects = append(rejects, AttachmentReject{Path: r.Path, Reason: reason})
				continue
			}
			media = append(media, ref)
			continue
		}
		if r.Inline != nil {
			ref, reason := ingestInlineAttachment(r.Inline)
			if reason != "" {
				rejects = append(rejects, AttachmentReject{Path: r.Path, Reason: reason})
				continue
			}
			media = append(media, ref)
			continue
		}
		rejects = append(rejects, AttachmentReject{Path: r.Path, Reason: "empty attachment"})
	}
	return media, rejects
}

func ingestAttachmentPath(outCwd, p string) (ref llmMediaRef, reason string) {
	abs, err := resolveAttachmentPath(outCwd, p)
	if err != nil {
		return llmMediaRef{}, err.Error()
	}
	st, err := os.Stat(abs)
	if err != nil {
		return llmMediaRef{}, fmt.Sprintf("%v", err)
	}
	if st.IsDir() {
		return llmMediaRef{}, "is a directory"
	}
	if st.Size() > maxAttachmentBytes() {
		return llmMediaRef{}, fmt.Sprintf("%d bytes exceeds max %d", st.Size(), maxAttachmentBytes())
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return llmMediaRef{}, fmt.Sprintf("%v", err)
	}
	m := sniffMIME(data)
	if !allowedAttachmentMIME(m) {
		return llmMediaRef{}, fmt.Sprintf("MIME %q not allowed (png/jpeg/webp/gif)", m)
	}
	return llmMediaRef{
		ID:   filepath.Base(abs),
		MIME: m,
		Data: base64.StdEncoding.EncodeToString(data),
	}, ""
}

func ingestInlineAttachment(in *InlineAttachment) (ref llmMediaRef, reason string) {
	mime := strings.ToLower(strings.TrimSpace(in.MIME))
	if !allowedAttachmentMIME(mime) {
		return llmMediaRef{}, fmt.Sprintf("MIME %q not allowed (png/jpeg/webp/gif)", mime)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(in.Base64))
	if err != nil {
		return llmMediaRef{}, "inline base64 decode failed"
	}
	if int64(len(raw)) > maxAttachmentBytes() {
		return llmMediaRef{}, fmt.Sprintf("%d bytes exceeds max %d", len(raw), maxAttachmentBytes())
	}
	return llmMediaRef{
		ID:   "inline-" + shortHash(raw),
		MIME: mime,
		Data: base64.StdEncoding.EncodeToString(raw),
	}, ""
}

// shortHash 生成内联附件的短 id（用于记忆摘要与日志 label，不保证全局唯一）。
func shortHash(b []byte) string {
	h := fnv.New32a()
	_, _ = h.Write(b)
	return fmt.Sprintf("%08x", h.Sum32())
}

// llmMediaRef 附件引用（轻量拷贝，避免 server 依赖 llm.MediaRef 的 json tag 细节）。
type llmMediaRef struct {
	ID   string
	MIME string
	Data string
}

// resolveAttachmentPath 解析附件路径：产出区内相对/绝对，或 attachment_dir 白名单下。
func resolveAttachmentPath(outCwd, p string) (string, error) {
	in := strings.TrimSpace(p)
	p = filepath.Clean(in)
	// 相对路径 → 产出区下。
	if !filepath.IsAbs(p) {
		return brain.PathUnderBase(outCwd, p)
	}
	// 绝对路径：允许产出区内或 attachment_dir 白名单。
	if outCwd != "" && isUnder(outCwd, p) {
		return p, nil
	}
	if dir := attachmentDir(); dir != "" && isUnder(dir, p) {
		return p, nil
	}
	return "", fmt.Errorf("attachment %s: 不在产出区或 attachment_dir 白名单内", in)
}

// isUnder base 前缀判定（绝对路径）。
func isUnder(base, p string) bool {
	rel, err := filepath.Rel(filepath.Clean(base), filepath.Clean(p))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func attachmentDir() string {
	if cfg := config.Config; cfg != nil {
		return strings.TrimSpace(cfg.LLM.AttachmentDir)
	}
	return ""
}

// maxAttachmentBytes 单张图片上限（默认 10MiB）。
func maxAttachmentBytes() int64 {
	return 10 * 1024 * 1024
}

// sniffMIME 从文件头探测 MIME（无扩展名依赖）。
func sniffMIME(data []byte) string {
	m := http.DetectContentType(data)
	if mt, _, err := mime.ParseMediaType(m); err == nil {
		return mt
	}
	return m
}

func allowedAttachmentMIME(m string) bool {
	switch strings.ToLower(m) {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return true
	}
	return false
}

// sanitizeAttachmentsForMemory 生成 short-term 记忆里的附件摘要（不写 base64）。
func sanitizeAttachmentsForMemory(media []llmMediaRef) string {
	if len(media) == 0 {
		return ""
	}
	names := make([]string, 0, len(media))
	for _, m := range media {
		names = append(names, m.ID)
	}
	return "[attachments: " + strings.Join(names, ", ") + "]"
}
