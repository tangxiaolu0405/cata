package brain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cata/internal/cata/clock"
)

// ResolveWorkspaceID 纯解析 cwd 对应的工作空间 id，不修改任何全局状态/注册表。
// 客户端用它在本机定位「一个工作空间一个 agent」的 per-ws socket。
func ResolveWorkspaceID(clientCwd string) (string, error) {
	if err := EnsureCataLayout(); err != nil {
		return "", err
	}
	out := strings.TrimSpace(clientCwd)
	if out == "" {
		var err error
		out, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	outputCwd, err := filepath.Abs(out)
	if err != nil {
		return "", err
	}
	focus, _, err := resolveFocusPath(outputCwd)
	if err != nil {
		return "", err
	}
	if ent, err := findRegistryByRoot(focus); err != nil {
		return "", err
	} else if ent != nil {
		return ent.ID, nil
	}
	return workspaceID(focus), nil
}

// WorkspaceByID 按 id 加载工作区（无副作用：不做 scaffold、不改全局状态）。
// 找不到返回 (nil, nil)。
func WorkspaceByID(wsID string) (*Workspace, error) {
	wsID = strings.TrimSpace(wsID)
	if wsID == "" {
		return nil, nil
	}
	entries, err := ListRegistryEntries()
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if entries[i].ID == wsID {
			ws := entryToWorkspace(&entries[i])
			if st, err := os.Stat(ws.RootPath); err == nil && st.IsDir() {
				return ws, nil
			}
		}
	}
	// 兜底：home 格 meta
	info, ok := FindHomeWorkspace(wsID)
	if !ok {
		return nil, nil
	}
	return &Workspace{
		ID:         info.ID,
		RootPath:   info.RootPath,
		Kind:       info.Kind,
		Name:       info.Name,
		ActiveMode: ModeDefaultID,
	}, nil
}

// BindWorkspace 单工作空间绑定：进程启动时按 id 固定一个工作空间并设为 Active，
// 产出目录指向 ws root。进程生命周期内不再跨工作空间切换——从结构上消除
// 多空间并行时的全局状态串扰（`cata agent --workspace <id>` 的语义）。
func BindWorkspace(wsID string) (*Workspace, error) {
	wsID = strings.TrimSpace(wsID)
	if wsID == "" {
		return nil, fmt.Errorf("bind: empty workspace id")
	}
	ws, err := WorkspaceByID(wsID)
	if err != nil {
		return nil, err
	}
	if ws == nil {
		return nil, fmt.Errorf("bind: workspace %q not found (run `cata chat` in the project or `cata link add --dir <path>` first)", wsID)
	}
	if err := ws.EnsureScaffold(); err != nil {
		return nil, err
	}
	SetActive(ws)
	SetOutputCwd(ws.RootPath)
	syncOutputDir(ws.RootPath)
	touchRegistryEntry(ws.ID)
	_ = upsertRegistryEntry(workspaceToEntry(ws))
	return ws, nil
}

// TouchRegistry 更新某工作区 last_seen（supervisor / link 注册时调用）。
func TouchRegistry(wsID string) error {
	ws, err := WorkspaceByID(wsID)
	if err != nil {
		return err
	}
	if ws == nil {
		return fmt.Errorf("workspace %q not found", wsID)
	}
	touchRegistryEntry(ws.ID)
	return upsertRegistryEntry(RegistryEntry{
		ID:         ws.ID,
		RootPath:   ws.RootPath,
		Kind:       ws.Kind,
		Name:       ws.Name,
		CreatedAt:  clock.RFC3339(),
		LastSeenAt: clock.RFC3339(),
		ActiveMode: ws.ActiveMode,
	})
}
