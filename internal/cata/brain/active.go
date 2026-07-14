package brain

import "sync"

var (
	activeMu sync.RWMutex
	activeWS *Workspace
)

// SetActive 设置当前请求上下文的工作区。
func SetActive(w *Workspace) {
	activeMu.Lock()
	activeWS = w
	activeMu.Unlock()
}

// Active 返回当前工作区；未设置时 nil。
func Active() *Workspace {
	activeMu.RLock()
	defer activeMu.RUnlock()
	return activeWS
}

// MustActive 返回当前工作区；若无则尝试用 cwd 解析。
func MustActive() (*Workspace, error) {
	if w := Active(); w != nil {
		return w, nil
	}
	return ResolveWorkspace("")
}
