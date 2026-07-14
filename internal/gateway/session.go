package gateway

import (
	"sync"
)

// SessionKey 渠道会话键（channel:chat_id，如 telegram:12345）。
type SessionKey string

// SessionManager 为每个渠道会话维护一条 cata socket（server 侧保留 history）。
// 各会话 cwd = {worker_root}/{channel}/{chat_id}/。
type SessionManager struct {
	socketPath string
	workerRoot string

	mu       sync.Mutex
	sessions map[SessionKey]*CataConn
}

// NewSessionManager 创建会话管理器。
func NewSessionManager(socketPath, workerRoot string) *SessionManager {
	return &SessionManager{
		socketPath: socketPath,
		workerRoot: workerRoot,
		sessions:   make(map[SessionKey]*CataConn),
	}
}

// Get 获取或创建会话连接（按会话键分配独立 worker 目录）。
func (m *SessionManager) Get(key SessionKey) (*CataConn, error) {
	cwd, err := WorkerCwdForSession(m.workerRoot, key)
	if err != nil {
		return nil, err
	}
	return m.GetWithCwd(key, cwd)
}

// GetWithCwd 获取或创建会话连接，使用显式 cwd（如 web 项目真实路径）。
func (m *SessionManager) GetWithCwd(key SessionKey, cwd string) (*CataConn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.sessions[key]; ok {
		if c.cwd == cwd {
			return c, nil
		}
		_ = c.Close()
		delete(m.sessions, key)
	}
	c := NewCataConn(m.socketPath, cwd)
	m.sessions[key] = c
	return c, nil
}

// Reset 重置指定会话。
func (m *SessionManager) Reset(key SessionKey) error {
	c, err := m.Get(key)
	if err != nil {
		return err
	}
	return c.Reset()
}

// CloseAll 关闭全部连接。
func (m *SessionManager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, c := range m.sessions {
		_ = c.Close()
		delete(m.sessions, k)
	}
}

// ProcessLock 串行化同一会话的消息处理（cata 单连接单轮 chat）。
type ProcessLock struct {
	mu    sync.Mutex
	locks map[SessionKey]*sync.Mutex
}

// NewProcessLock 创建 per-session 处理锁。
func NewProcessLock() *ProcessLock {
	return &ProcessLock{locks: make(map[SessionKey]*sync.Mutex)}
}

// Lock 锁定会话；返回 unlock 函数。
func (p *ProcessLock) Lock(key SessionKey) func() {
	p.mu.Lock()
	l, ok := p.locks[key]
	if !ok {
		l = &sync.Mutex{}
		p.locks[key] = l
	}
	p.mu.Unlock()
	l.Lock()
	return l.Unlock
}
