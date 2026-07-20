package brain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cata/internal/cata/clock"
)

// WorkspaceKind 工作区类型。
type WorkspaceKind string

const (
	KindGit       WorkspaceKind = "git"
	KindMarked    WorkspaceKind = "marked"
	KindEphemeral WorkspaceKind = "ephemeral"
)

// RegistryEntry 注册表中的一条工作区记录。
type RegistryEntry struct {
	ID          string        `json:"id"`
	RootPath    string        `json:"root_path"`
	Kind        WorkspaceKind `json:"kind"`
	Name        string        `json:"name,omitempty"`
	CreatedAt   string        `json:"created_at"`
	LastSeenAt  string        `json:"last_seen_at"`
	ActiveMode  string        `json:"active_mode"`
}

type workspacesRegistryData struct {
	Workspaces []RegistryEntry `json:"workspaces"`
}

var registryMu sync.Mutex

func loadRegistry() (*workspacesRegistryData, error) {
	path := workspacesRegistryPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &workspacesRegistryData{Workspaces: []RegistryEntry{}}, nil
		}
		return nil, err
	}
	var rf workspacesRegistryData
	if err := json.Unmarshal(data, &rf); err != nil {
		return nil, err
	}
	if rf.Workspaces == nil {
		rf.Workspaces = []RegistryEntry{}
	}
	return &rf, nil
}

func saveRegistry(rf *workspacesRegistryData) error {
	if err := os.MkdirAll(registryDir(), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(workspacesRegistryPath(), data, 0644)
}

func upsertRegistryEntry(e RegistryEntry) error {
	registryMu.Lock()
	defer registryMu.Unlock()
	rf, err := loadRegistry()
	if err != nil {
		return err
	}
	found := false
	for i := range rf.Workspaces {
		if rf.Workspaces[i].ID == e.ID {
			rf.Workspaces[i] = e
			found = true
			break
		}
	}
	if !found {
		rf.Workspaces = append(rf.Workspaces, e)
	}
	return saveRegistry(rf)
}

// ListRegistryEntries 返回所有已注册工作区。
func ListRegistryEntries() ([]RegistryEntry, error) {
	registryMu.Lock()
	defer registryMu.Unlock()
	rf, err := loadRegistry()
	if err != nil {
		return nil, err
	}
	out := make([]RegistryEntry, len(rf.Workspaces))
	copy(out, rf.Workspaces)
	return out, nil
}

// RecentlyActive 判断 last_seen_at 是否在 within 窗口内（空或无效时间戳视为不活跃）。
func (e RegistryEntry) RecentlyActive(within time.Duration) bool {
	if within <= 0 {
		within = DefaultEvolveActiveWindow
	}
	s := strings.TrimSpace(e.LastSeenAt)
	if s == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return false
	}
	return clock.Now().Sub(t) <= within
}

func touchRegistryEntry(id string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	rf, err := loadRegistry()
	if err != nil {
		return
	}
	now := clock.RFC3339()
	for i := range rf.Workspaces {
		if rf.Workspaces[i].ID == id {
			rf.Workspaces[i].LastSeenAt = now
			_ = saveRegistry(rf)
			return
		}
	}
}

func findRegistryByRoot(root string) (*RegistryEntry, error) {
	root = filepath.Clean(root)
	rf, err := loadRegistry()
	if err != nil {
		return nil, err
	}
	for i := range rf.Workspaces {
		if filepath.Clean(rf.Workspaces[i].RootPath) == root {
			e := rf.Workspaces[i]
			return &e, nil
		}
	}
	return nil, nil
}
