package server

import (
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"mime"
	"net/http"
	"os"
	"os/exec"
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

// attachmentDoc 从 PDF 提取出的文本附件（不依赖模型 document modality）。
type attachmentDoc struct {
	Name string
	Text string
}

// ingestAttachments 把客户端随 chat 发送的附件读取为带 base64 的 MediaRef。
// 两种来源：path（相对产出区 / attachment_dir 白名单的本地文件）与 inline（剪贴板粘贴等已编码内容）。
// 三类产出：图片/音频进 media（出站按能力编码）；PDF 提取文本进 docs（拼入 user 正文，
// 任何文本模型可读）；逐条失败进 rejects（调用方发 attachment_rejected 事件），合法项继续。
func ingestAttachments(outCwd string, reqs []AttachmentReq) (media []llmMediaRef, docs []attachmentDoc, rejects []AttachmentReject) {
	for _, r := range reqs {
		hasPath := strings.TrimSpace(r.Path) != ""
		if hasPath {
			ref, doc, reason := ingestAttachmentPath(outCwd, strings.TrimSpace(r.Path))
			if reason != "" {
				rejects = append(rejects, AttachmentReject{Path: r.Path, Reason: reason})
				continue
			}
			if doc != nil {
				docs = append(docs, *doc)
				continue
			}
			media = append(media, ref)
			continue
		}
		if r.Inline != nil {
			ref, doc, reason := ingestInlineAttachment(r.Inline)
			if reason != "" {
				rejects = append(rejects, AttachmentReject{Path: r.Path, Reason: reason})
				continue
			}
			if doc != nil {
				docs = append(docs, *doc)
				continue
			}
			media = append(media, ref)
			continue
		}
		rejects = append(rejects, AttachmentReject{Path: r.Path, Reason: "empty attachment"})
	}
	return media, docs, rejects
}

func ingestAttachmentPath(outCwd, p string) (ref llmMediaRef, doc *attachmentDoc, reason string) {
	abs, err := resolveAttachmentPath(outCwd, p)
	if err != nil {
		return llmMediaRef{}, nil, err.Error()
	}
	st, err := os.Stat(abs)
	if err != nil {
		return llmMediaRef{}, nil, fmt.Sprintf("%v", err)
	}
	if st.IsDir() {
		return llmMediaRef{}, nil, "is a directory"
	}
	if st.Size() > maxAttachmentBytes() {
		return llmMediaRef{}, nil, fmt.Sprintf("%d bytes exceeds max %d", st.Size(), maxAttachmentBytes())
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return llmMediaRef{}, nil, fmt.Sprintf("%v", err)
	}
	m := sniffMIME(data)
	if m == "application/pdf" {
		return ingestPDFData(filepath.Base(abs), data)
	}
	if !allowedAttachmentMIME(m) {
		return llmMediaRef{}, nil, fmt.Sprintf("MIME %q not allowed (png/jpeg/webp/gif; pdf 走文本提取)", m)
	}
	return llmMediaRef{
		ID:   filepath.Base(abs),
		MIME: m,
		Data: base64.StdEncoding.EncodeToString(data),
	}, nil, ""
}

func ingestInlineAttachment(in *InlineAttachment) (ref llmMediaRef, doc *attachmentDoc, reason string) {
	mime := strings.ToLower(strings.TrimSpace(in.MIME))
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(in.Base64))
	if err != nil {
		return llmMediaRef{}, nil, "inline base64 decode failed"
	}
	if int64(len(raw)) > maxAttachmentBytes() {
		return llmMediaRef{}, nil, fmt.Sprintf("%d bytes exceeds max %d", len(raw), maxAttachmentBytes())
	}
	if mime == "application/pdf" {
		return ingestPDFData("inline-pdf", raw)
	}
	if !allowedAttachmentMIME(mime) {
		return llmMediaRef{}, nil, fmt.Sprintf("MIME %q not allowed (png/jpeg/webp/gif; pdf 走文本提取)", mime)
	}
	return llmMediaRef{
		ID:   "inline-" + shortHash(raw),
		MIME: mime,
		Data: base64.StdEncoding.EncodeToString(raw),
	}, nil, ""
}

// pdfMaxExtractBytes 单份 PDF 提取文本上限（防畸形 PDF 生成的文本撑爆上下文）。
const pdfMaxExtractBytes = 256 * 1024

// ingestPDFData 用系统 pdftotext（poppler）提取 PDF 文本，作为 attachmentDoc 返回。
// pdftotext 缺失时按「无法提取」拒绝，并给出可安装提示；提取为空也是合法（扫描版 PDF）。
func ingestPDFData(name string, data []byte) (ref llmMediaRef, doc *attachmentDoc, reason string) {
	path, err := exec.LookPath("pdftotext")
	if err != nil {
		return llmMediaRef{}, nil,
			"PDF 文本提取需要 pdftotext（poppler-utils），当前不可用；请安装后重试，或先把 PDF 转成文本/图片"
	}
	// 写入临时文件（pdftotext 按路径读，不支持 stdin 直读）。
	tmp, err := os.CreateTemp("", "cata-pdf-*.pdf")
	if err != nil {
		return llmMediaRef{}, nil, fmt.Sprintf("pdf temp: %v", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return llmMediaRef{}, nil, fmt.Sprintf("pdf temp write: %v", err)
	}
	_ = tmp.Close()

	cmd := exec.Command(path, tmpPath, "-")
	out, err := cmd.Output()
	if err != nil {
		return llmMediaRef{}, nil, fmt.Sprintf("pdftotext: %v", err)
	}
	text := strings.TrimSpace(string(out))
	if len(text) > pdfMaxExtractBytes {
		text = text[:pdfMaxExtractBytes] + "\n…(截断)"
	}
	return llmMediaRef{}, &attachmentDoc{Name: name, Text: text}, ""
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
