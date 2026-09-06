package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"cata/internal/cata/config"
)

// SessionCwdStore 持久化每个渠道会话的产出区切换（/dir）。
// gateway 重启后自动恢复；再次 /dir 切换或 /dir reset 才改变。
// 存储文件：CATA_HOME/gateway_session_cwd.json（key = "channel:chat_id"）。
type SessionCwdStore struct {
	mu   sync.Mutex
	path string
	data map[SessionKey]string
}

// NewSessionCwdStore 从指定路径加载（文件缺失视为空）。
func NewSessionCwdStore(path string) *SessionCwdStore {
	s := &SessionCwdStore{path: path, data: map[SessionKey]string{}}
	if path != "" {
		if raw, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(raw, &s.data)
			if s.data == nil {
				s.data = map[SessionKey]string{}
			}
		}
	}
	return s
}

// DefaultSessionCwdStore 默认存储：~/.cata/gateway_session_cwd.json。
func DefaultSessionCwdStore() *SessionCwdStore {
	return NewSessionCwdStore(filepath.Join(config.CataHome(), "gateway_session_cwd.json"))
}

// Get 该会话持久化的产出区（空 = 未切换，用默认 worker 目录）。
func (s *SessionCwdStore) Get(key SessionKey) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[key]
}

// Set 记录/更新（cwd 为空 = 删除记录，回到默认 worker 目录）；立即落盘。
func (s *SessionCwdStore) Set(key SessionKey, cwd string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cwd = filepath.Clean(cwd)
	if cwd == "" || cwd == "." {
		delete(s.data, key)
	} else {
		s.data[key] = cwd
	}
	s.save()
}

func (s *SessionCwdStore) save() {
	if s.path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(s.path, raw, 0644)
}
