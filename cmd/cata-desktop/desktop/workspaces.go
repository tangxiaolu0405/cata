package desktop

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"cata/internal/cata/config"
	"cata/internal/cata/link"
)

// WorkspaceInfo 一个工作空间（agent 的产出区 / root_path）。
type WorkspaceInfo struct {
	ID       string `json:"id"`                // link.json 的 agent_id；手动添加为空
	Name     string `json:"name"`              // 显示名
	RootPath string `json:"root_path"`         // 绝对路径
	Linked   bool   `json:"linked"`            // 是否 link.json 注册（常驻 agent）
	Source   string `json:"source"`            // "link" | "extra"
	Running  bool   `json:"running,omitempty"` // 预留：对应 agent 是否在跑
}

// FileEntry 目录树的一项。
type FileEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size,omitempty"`
	ModTime string `json:"mod_time,omitempty"`
}

// FileContent 读取结果。
type FileContent struct {
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"` // 超过 maxReadBytes 被截断
	Binary    bool   `json:"binary"`
	Size      int64  `json:"size"`
}

// desktopConfig 桌面端自己的「额外目录」列表（放在 CATA_HOME/desktop.json，
// 不碰 link.json，也不修改 cata 核心）。
type desktopConfig struct {
	Extras []string `json:"extras,omitempty"`
}

func desktopConfigPath() string { return filepath.Join(config.CataHome(), "desktop.json") }

func loadDesktopConfig() desktopConfig {
	var cfg desktopConfig
	data, err := os.ReadFile(desktopConfigPath())
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(data, &cfg)
	return cfg
}

func saveDesktopConfig(cfg desktopConfig) error {
	if err := os.MkdirAll(config.CataHome(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := desktopConfigPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, desktopConfigPath())
}

// ListWorkspaces 列出所有工作空间：link.json 注册的（Linked）在前，
// 手动添加的目录在后；目录不存在或不可读的自动跳过。
func (a *App) ListWorkspaces() ([]WorkspaceInfo, error) {
	seen := map[string]bool{}
	out := make([]WorkspaceInfo, 0) // 非 nil：Wails 会把 Go nil slice 序列化成 JS null

	add := func(id, name, root string, linked bool, source string) {
		root = filepath.Clean(root)
		if root == "" || root == "." || seen[root] {
			return
		}
		st, err := os.Stat(root)
		if err != nil || !st.IsDir() {
			return
		}
		seen[root] = true
		if name == "" {
			name = filepath.Base(root)
		}
		out = append(out, WorkspaceInfo{
			ID:       id,
			Name:     name,
			RootPath: root,
			Linked:   linked,
			Source:   source,
		})
	}

	if cfg, err := link.LoadConfig(); err == nil {
		for id, e := range cfg.Agents {
			if !e.Enabled {
				continue
			}
			add(id, e.Name, e.RootPath, true, "link")
		}
	}
	for _, p := range loadDesktopConfig().Extras {
		add("", filepath.Base(p), p, false, "extra")
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Linked != out[j].Linked {
			return out[i].Linked // 注册的工作空间在前
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// AddWorkspace 手动添加一个本地目录作为工作空间（写 desktop.json）。
func (a *App) AddWorkspace(path string) error {
	path = filepath.Clean(path)
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s 不是目录", path)
	}
	cfg := loadDesktopConfig()
	for _, p := range cfg.Extras {
		if filepath.Clean(p) == path {
			return nil // 已存在
		}
	}
	cfg.Extras = append(cfg.Extras, path)
	return saveDesktopConfig(cfg)
}

// RemoveWorkspace 移除手动添加的工作空间（只影响 desktop.json）。
func (a *App) RemoveWorkspace(path string) error {
	path = filepath.Clean(path)
	cfg := loadDesktopConfig()
	keep := cfg.Extras[:0]
	for _, p := range cfg.Extras {
		if filepath.Clean(p) != path {
			keep = append(keep, p)
		}
	}
	cfg.Extras = keep
	return saveDesktopConfig(cfg)
}

// ListDir 列出目录内容：目录在前，按名称排序（隐藏文件也显示，.git 除外）。
func (a *App) ListDir(path string) ([]FileEntry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		fe := FileEntry{
			Name:    e.Name(),
			Path:    filepath.Join(path, e.Name()),
			IsDir:   e.IsDir(),
			ModTime: info.ModTime().Format("2006-01-02 15:04"),
		}
		if !e.IsDir() {
			fe.Size = info.Size()
		}
		out = append(out, fe)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// maxReadBytes 文本预览上限（512KB），超长截断并标记。
const maxReadBytes = 512 * 1024

// ReadFile 读取文件内容（文本为主；检测到二进制返回 Binary 标记，不展示内容）。
func (a *App) ReadFile(path string) (FileContent, error) {
	st, err := os.Stat(path)
	if err != nil {
		return FileContent{}, err
	}
	if st.IsDir() {
		return FileContent{}, fmt.Errorf("%s 是目录", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return FileContent{}, err
	}
	defer f.Close()

	buf := make([]byte, maxReadBytes+1)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return FileContent{}, err
	}
	buf = buf[:n]
	truncated := n > maxReadBytes
	if truncated {
		buf = buf[:maxReadBytes]
	}
	return FileContent{
		Content:   string(buf),
		Truncated: truncated,
		Binary:    looksBinary(buf),
		Size:      st.Size(),
	}, nil
}

// looksBinary 简单的二进制检测：前 8KB 内出现 NUL 字节即视为二进制。
func looksBinary(b []byte) bool {
	lim := len(b)
	if lim > 8000 {
		lim = 8000
	}
	for _, c := range b[:lim] {
		if c == 0 {
			return true
		}
	}
	return false
}
