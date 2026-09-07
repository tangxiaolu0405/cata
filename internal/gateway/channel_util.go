package gateway

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cata/internal/cata/config"
)

// ChannelStatus 构建渠道 /status 回复（TG/QQ 共用）。
// 显示：当前工作空间（/dir 切换或默认）、LLM provider/model。
func ChannelStatus(sessions *SessionManager, cfg Config, channel string, key SessionKey) string {
	var b strings.Builder
	b.WriteString("Cata Gateway 状态\n")
	b.WriteString("──────────────\n")

	// 当前工作空间：/dir 切换的产出区优先，否则默认转发目标。
	if dir := sessions.CwdOverride(key); dir != "" {
		fmt.Fprintf(&b, "工作空间: %s\n", dir)
	} else if agentID, root, ok := sessions.ResolveForwardTarget(cfg); ok {
		fmt.Fprintf(&b, "工作空间: %s\n", root)
		fmt.Fprintf(&b, "转发 agent: %s\n", agentID)
	} else {
		b.WriteString("工作空间: (无可用目标 —— 请 /dir 选择或确认已注册工作空间 agent)\n")
	}

	// LLM provider/model（读 ~/.cata/config.json）。
	if llm := config.ResolvedLLMLabel(); llm != "" {
		fmt.Fprintf(&b, "LLM: %s\n", llm)
	} else {
		b.WriteString("LLM: (未配置)\n")
	}

	b.WriteString("\n命令: /help /clear /dir /status")
	return b.String()
}

// DownloadToTempDir 把远程文件下载到目标目录，返回本地路径。
// 用于渠道附件：下载到 worker 产出区（或临时目录）后作为 AttachmentReq.Path，
// 由 server 侧 ingest 校验并注入（路径须在产出区或 llm.attachment_dir 白名单内）。
func DownloadToTempDir(ctx context.Context, url, dir, filename string) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	path := filepath.Join(dir, SanitizeFilename(filename))
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, io.LimitReader(resp.Body, 20*1024*1024)); err != nil {
		return "", err
	}
	return path, nil
}

// SanitizeFilename 移除文件名中的路径分隔符与危险字符（导出供渠道使用）。
func SanitizeFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '\x00':
			return '_'
		}
		return r
	}, name)
	if name == "" || name == "." || name == ".." {
		return "attachment"
	}
	return name
}

// TempAttachmentDir 返回渠道附件临时目录（~/.cata/gateway_attachments/<channel>）。
// 注意：此目录在产出区外，server 读取需在 llm.attachment_dir 白名单内；
// 推荐渠道附件直接下载到 worker 产出区（~/.cata_worker/<channel>/<chat_id>/）。
func TempAttachmentDir(channel string) (string, error) {
	dir := filepath.Join(config.CataHome(), "gateway_attachments", channel)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}
