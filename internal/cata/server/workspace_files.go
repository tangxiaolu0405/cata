package server

import (
	"context"
	"fmt"
	"os"
	"strings"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
	"cata/internal/cata/protocol"
	"cata/internal/llm"
)

func workspaceFileLimits() (maxRead, maxWrite int) {
	maxRead, maxWrite = 512*1024, 512*1024
	if config.Config != nil {
		wf := config.Config.WorkspaceFiles
		if wf.MaxReadBytes > 0 {
			maxRead = wf.MaxReadBytes
		}
		if wf.MaxWriteBytes > 0 {
			maxWrite = wf.MaxWriteBytes
		}
	}
	return maxRead, maxWrite
}

func resolveWorkspaceFile(ctx context.Context, rel string) (string, error) {
	cc := brain.ChatContextFrom(ctx)
	return brain.ResolveChatFilePathFor(cc.WS, cc.OutputCwd, rel)
}

func toolReadFile(ctx context.Context, argsJSON string) (string, error) {
	if err := protocol.ContextErr(ctx); err != nil {
		return "", err
	}
	var p struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := llm.ParseToolArguments(argsJSON, &p); err != nil {
		return "", fmt.Errorf("read_file args: %w", err)
	}
	if strings.TrimSpace(p.Path) == "" {
		return "", fmt.Errorf("read_file: path required")
	}
	full, err := resolveWorkspaceFile(ctx, p.Path)
	if err != nil {
		return "", err
	}
	maxRead, _ := workspaceFileLimits()
	data, err := protocol.ReadFileWithContext(ctx, full, maxRead+1)
	if err != nil {
		return "", err
	}
	text := string(data)
	if len(data) > maxRead {
		text = text[:maxRead] + "\n…(truncated by max_read_bytes)"
	}
	if p.Offset > 0 || p.Limit > 0 {
		lines := strings.Split(text, "\n")
		start := p.Offset
		if start < 1 {
			start = 1
		}
		if start > len(lines) {
			return fmt.Sprintf("read %s: offset %d beyond end (%d lines)", p.Path, start, len(lines)), nil
		}
		slice := lines[start-1:]
		if p.Limit > 0 && len(slice) > p.Limit {
			slice = slice[:p.Limit]
		}
		text = strings.Join(slice, "\n")
	}
	return fmt.Sprintf("read %s resolved=%s (%d bytes shown)\n%s", p.Path, full, len(text), text), nil
}

func toolListFiles(ctx context.Context, argsJSON string) (string, error) {
	if err := protocol.ContextErr(ctx); err != nil {
		return "", err
	}
	var p struct {
		Path string `json:"path"`
	}
	if err := llm.ParseToolArguments(argsJSON, &p); err != nil {
		return "", fmt.Errorf("list_files args: %w", err)
	}
	rel := strings.TrimSpace(p.Path)
	if rel == "" {
		rel = "."
	}
	full, err := resolveWorkspaceFile(ctx, rel)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("list_files %s (%d entries)\n", rel, len(entries)))
	for i, e := range entries {
		if i&31 == 0 {
			if err := protocol.ContextErr(ctx); err != nil {
				return "", err
			}
		}
		info, err := e.Info()
		size := int64(0)
		isDir := " "
		if err == nil {
			size = info.Size()
			if e.IsDir() {
				isDir = "d"
			}
		}
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		b.WriteString(fmt.Sprintf("  %s %10d  %s\n", isDir, size, name))
	}
	return b.String(), nil
}

func toolSearchReplace(ctx context.Context, argsJSON string) (string, error) {
	var p struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := llm.ParseToolArguments(argsJSON, &p); err != nil {
		return "", fmt.Errorf("search_replace args: %w", err)
	}
	if strings.TrimSpace(p.Path) == "" {
		return "", fmt.Errorf("search_replace: path required")
	}
	if p.OldString == "" {
		return "", fmt.Errorf("search_replace: old_string required")
	}
	full, err := resolveWorkspaceFile(ctx, p.Path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	_, maxWrite := workspaceFileLimits()
	content := string(data)
	var newContent string
	var n int
	if p.ReplaceAll {
		newContent = strings.ReplaceAll(content, p.OldString, p.NewString)
		n = strings.Count(content, p.OldString)
	} else {
		idx := strings.Index(content, p.OldString)
		if idx < 0 {
			return "", fmt.Errorf("search_replace: old_string not found in %s", p.Path)
		}
		newContent = content[:idx] + p.NewString + content[idx+len(p.OldString):]
		n = 1
	}
	if newContent == content {
		return "", fmt.Errorf("search_replace: old_string not found in %s", p.Path)
	}
	if len(newContent) > maxWrite {
		return "", fmt.Errorf("search_replace: result exceeds max_write_bytes (%d)", maxWrite)
	}
	if err := os.WriteFile(full, []byte(newContent), 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("search_replace %s resolved=%s: %d replacement(s), %d -> %d bytes", p.Path, full, n, len(content), len(newContent)), nil
}

func toolAppendFile(ctx context.Context, argsJSON string) (string, error) {
	var p struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := llm.ParseToolArguments(argsJSON, &p); err != nil {
		return "", fmt.Errorf("append_file args: %w", err)
	}
	if strings.TrimSpace(p.Path) == "" {
		return "", fmt.Errorf("append_file: path required")
	}
	full, err := resolveWorkspaceFile(ctx, p.Path)
	if err != nil {
		return "", err
	}
	_, maxWrite := workspaceFileLimits()
	add := len(p.Content)
	if add > maxWrite {
		return "", fmt.Errorf("append_file: content exceeds max_write_bytes (%d)", maxWrite)
	}
	var prev int64
	if st, err := os.Stat(full); err == nil {
		prev = st.Size()
		if prev+int64(add) > int64(maxWrite) {
			return "", fmt.Errorf("append_file: file would exceed max_write_bytes")
		}
	}
	f, err := os.OpenFile(full, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	n, err := f.WriteString(p.Content)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("append_file %s resolved=%s: appended %d bytes (was %d)", p.Path, full, n, prev), nil
}
