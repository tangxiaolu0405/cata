package brain

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	capsCacheMu  sync.RWMutex
	capsCacheKey string
	capsCacheVal Capabilities
)

// LoadActiveCapabilitiesCached 带文件 mtime 缓存的 capabilities 读取。
func LoadActiveCapabilitiesCached() Capabilities {
	return LoadCapabilitiesCachedFor(Active())
}

// LoadCapabilitiesCachedFor 显式指定 workspace 的带缓存 capabilities 读取。
func LoadCapabilitiesCachedFor(w *Workspace) Capabilities {
	key := capabilitiesCacheKeyFor(w)
	capsCacheMu.RLock()
	if capsCacheKey == key {
		c := capsCacheVal
		capsCacheMu.RUnlock()
		return c
	}
	capsCacheMu.RUnlock()

	c := LoadCapabilitiesFor(w)
	capsCacheMu.Lock()
	capsCacheKey = key
	capsCacheVal = c
	capsCacheMu.Unlock()
	return c
}

func capabilitiesCacheKey() string {
	return capabilitiesCacheKeyFor(Active())
}

func capabilitiesCacheKeyFor(w *Workspace) string {
	if w == nil {
		return "no-workspace"
	}
	path := filepath.Join(w.ModeDir(w.modeID()), FileCapabilities)
	return w.ID + "|" + w.modeID() + "|" + fmt.Sprintf("%d", fileModTimeNano(path))
}

func fileModTimeNano(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.ModTime().UnixNano()
}
