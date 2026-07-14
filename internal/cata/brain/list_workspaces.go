package brain

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// HomeWorkspaceInfo 列出 ~/.cata/brain/workspaces 下一格的摘要（UI / 注册表）。
type HomeWorkspaceInfo struct {
	ID         string        `json:"id"`
	Name       string        `json:"name,omitempty"`
	RootPath   string        `json:"root_path"`
	Kind       WorkspaceKind `json:"kind,omitempty"`
	LastSeenAt string        `json:"last_seen_at,omitempty"`
	HomeDir    string        `json:"home_dir"` // ~/.cata/brain/workspaces/<id>
}

// WorkspacesRoot 返回 ~/.cata/brain/workspaces。
func WorkspacesRoot() string {
	return workspacesRoot()
}

// ListHomeWorkspaces 扫描 home 脑子格目录（及 registry），返回真实项目 root_path。
// 默认跳过 cwd 落在 .cata_worker 下的渠道沙箱格。
func ListHomeWorkspaces() ([]HomeWorkspaceInfo, error) {
	root := workspacesRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	regByID := map[string]RegistryEntry{}
	if rf, err := ListRegistryEntries(); err == nil {
		for _, e := range rf {
			regByID[e.ID] = e
		}
	}

	out := make([]HomeWorkspaceInfo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		if id == "" || strings.HasPrefix(id, ".") {
			continue
		}
		info := HomeWorkspaceInfo{
			ID:      id,
			HomeDir: filepath.Join(root, id),
		}
		if meta := readWorkspaceMeta(filepath.Join(info.HomeDir, RelMetaJSON)); meta != nil {
			info.RootPath = meta["root_path"]
			info.Name = meta["name"]
			info.Kind = WorkspaceKind(meta["kind"])
		}
		if re, ok := regByID[id]; ok {
			if info.RootPath == "" {
				info.RootPath = re.RootPath
			}
			if info.Name == "" {
				info.Name = re.Name
			}
			if info.Kind == "" {
				info.Kind = re.Kind
			}
			info.LastSeenAt = re.LastSeenAt
		}
		info.RootPath = strings.TrimSpace(info.RootPath)
		if info.RootPath == "" {
			continue
		}
		if isChannelWorkerPath(info.RootPath) || isChannelWorkerPath(id) {
			continue
		}
		st, err := os.Stat(info.RootPath)
		if err != nil || !st.IsDir() {
			continue
		}
		if info.Name == "" {
			info.Name = filepath.Base(info.RootPath)
		}
		out = append(out, info)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].LastSeenAt != out[j].LastSeenAt {
			return out[i].LastSeenAt > out[j].LastSeenAt
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// FindHomeWorkspace 按 id 查找一格（含 root_path 存在性检查）。
func FindHomeWorkspace(id string) (HomeWorkspaceInfo, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return HomeWorkspaceInfo{}, false
	}
	list, err := ListHomeWorkspaces()
	if err != nil {
		return HomeWorkspaceInfo{}, false
	}
	for _, w := range list {
		if w.ID == id {
			return w, true
		}
	}
	return HomeWorkspaceInfo{}, false
}

func isChannelWorkerPath(p string) bool {
	s := strings.ToLower(filepath.ToSlash(p))
	return strings.Contains(s, ".cata_worker/") || strings.Contains(s, ".cata_worker\\") ||
		strings.Contains(s, "cata_worker-telegram") || strings.Contains(s, "cata_worker-qq") ||
		strings.Contains(s, "cata_worker/")
}
