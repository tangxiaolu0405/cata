package gateway

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"

	"cata/internal/gateway/tunnel"
)

// ConnFactory 创建一条 cata 连接句柄：本地模式拨本机 Unix socket，
// remote 模式经隧道拨远端 agent（socketPath 参数在 remote 模式被忽略）。
type ConnFactory func(socketPath, cwd string) *CataConn

// SessionKey 渠道会话键（channel:chat_id，如 telegram:12345）。
type SessionKey string

// SessionManager 为每个渠道会话维护一条 cata socket（server 侧保留 history）。
// 各会话 cwd = {worker_root}/{channel}/{chat_id}/。
type SessionManager struct {
	socketPath  string
	workerRoot  string
	connFactory ConnFactory
	// remote 远程模式（隧道拨远端 agent）：本地 per-ws 拉起/解析不适用。
	remote bool
	// cwdStore /dir 切换的持久化（重启恢复）；nil = 不持久化（测试用）。
	cwdStore *SessionCwdStore
	// dirListSeen 该会话是否已看过 /dir 工作区列表：未看列表不允许序号切换
	// （序号按最近使用排序，重启后可能变化，须先确认列表）。
	dirListSeen map[SessionKey]bool
	// remoteDial remote 模式下按绑定 agent 拨其隧道（nil = 退化为默认 connFactory）。
	remoteDial func(agentID string) func() (net.Conn, error)

	mu       sync.Mutex
	sessions map[SessionKey]*CataConn
}

// NewSessionManager 创建会话管理器（本地模式：拨本机 Unix socket）。
func NewSessionManager(socketPath, workerRoot string) *SessionManager {
	return NewSessionManagerWithStore(socketPath, workerRoot, DefaultSessionCwdStore())
}

// NewSessionManagerWithStore 指定持久化存储创建会话管理器（空 store = 不持久化）。
func NewSessionManagerWithStore(socketPath, workerRoot string, store *SessionCwdStore) *SessionManager {
	return &SessionManager{
		socketPath:  socketPath,
		workerRoot:  workerRoot,
		connFactory: NewCataConn,
		cwdStore:    store,
		dirListSeen: make(map[SessionKey]bool),
		sessions:    make(map[SessionKey]*CataConn),
	}
}

// NewRemoteSessionManager 创建会话管理器（remote 模式：经 connFactory 拨远端 agent）。
func NewRemoteSessionManager(workerRoot string, connFactory ConnFactory) *SessionManager {
	return NewRemoteSessionManagerWithStore(workerRoot, connFactory, DefaultSessionCwdStore())
}

// NewRemoteSessionManagerWithStore 指定持久化存储的 remote 会话管理器。
func NewRemoteSessionManagerWithStore(workerRoot string, connFactory ConnFactory, store *SessionCwdStore) *SessionManager {
	return &SessionManager{
		workerRoot:  workerRoot,
		connFactory: connFactory,
		remote:      true,
		cwdStore:    store,
		dirListSeen: make(map[SessionKey]bool),
		sessions:    make(map[SessionKey]*CataConn),
	}
}

// IsRemote 是否 remote（远端隧道）模式：本地 per-ws 拉起/解析逻辑不适用。
func (m *SessionManager) IsRemote() bool {
	return m.remote
}

// RemoteSessionManagerForDefaultAgent 创建 remote 模式通道会话管理器：
// 所有会话（telegram/qq）拨到指定 agent（v1：cfg.DefaultAgentID 或第一个在线 agent）。
// cwd 统一用该 agent 的工作空间根路径（远端真实存在；v1 通道会话共享产出区，history 仍 per-连接）。
// 目标 agent 懒解析：网关可以先于 agent 上线启动，连接建立时才选当前在线目标。
func RemoteSessionManagerForDefaultAgent(cfg Config, reg *tunnel.Registry) (*SessionManager, error) {
	if reg == nil {
		return nil, fmt.Errorf("remote registry required")
	}
	m := NewRemoteSessionManager(cfg.WorkerRoot, func(_ string, _ string) *CataConn {
		agentID, root := defaultAgentTarget(cfg, reg)
		return NewCataConnWithDialer("", root, func() (net.Conn, error) {
			return reg.DialAgent(agentID)
		})
	})
	// 按绑定 agent 拨隧道（渠道转发的首选；default 仅作未指定时兜底）。
	m.remoteDial = func(agentID string) func() (net.Conn, error) {
		if reg == nil || agentID == "" {
			return nil
		}
		return func() (net.Conn, error) {
			return reg.DialAgent(agentID)
		}
	}
	return m, nil
}

// defaultAgentTarget 返回当前默认通道 agent：优先 cfg.DefaultAgentID，否则第一个在线。
func defaultAgentTarget(cfg Config, reg *tunnel.Registry) (agentID, root string) {
	id := strings.TrimSpace(cfg.DefaultAgentID)
	if id != "" && reg.AgentAlive(id) {
		if info, ok := reg.FindAgent(id); ok {
			return id, info.RootPath
		}
	}
	agents := reg.OnlineAgents()
	if len(agents) == 0 {
		return "", ""
	}
	return agents[0].AgentID, agents[0].RootPath
}

// Get 获取或创建会话连接：
//   - 已有会话（含 /dir 切换过产出区的连接）→ 直接复用，保留其 cwd
//   - 未见过该会话 → 优先持久化的 /dir 切换（重启恢复），否则默认 worker 目录
//
// 断线由 socketclient 读写时自动重拨（dialFunc 重拨时重新 EnsureAgent）。
// 注意：不要在这里回退 worker 目录——那会把 /dir 切换的连接覆盖成原路径。
func (m *SessionManager) Get(key SessionKey) (*CataConn, error) {
	m.mu.Lock()
	c, ok := m.sessions[key]
	m.mu.Unlock()
	if ok {
		return c, nil
	}
	cwd := ""
	if m.cwdStore != nil {
		cwd = m.cwdStore.Get(key)
	}
	if cwd == "" {
		var err error
		cwd, err = WorkerCwdForSession(m.workerRoot, key)
		if err != nil {
			return nil, err
		}
	}
	return m.GetWithCwd(key, cwd)
}

// CwdOverride 该会话持久化的产出区（空 = 未切换）。
func (m *SessionManager) CwdOverride(key SessionKey) string {
	if m.cwdStore == nil {
		return ""
	}
	return m.cwdStore.Get(key)
}

// SetCwdOverride 持久化该会话的产出区切换（空 = 恢复默认）。
func (m *SessionManager) SetCwdOverride(key SessionKey, cwd string) {
	if m.cwdStore != nil {
		m.cwdStore.Set(key, cwd)
	}
}

// MarkDirListSeen 记录该会话已查看 /dir 工作区列表（之后才允许序号切换）。
func (m *SessionManager) MarkDirListSeen(key SessionKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dirListSeen[key] = true
}

// DirListSeen 该会话是否已查看 /dir 工作区列表。
func (m *SessionManager) DirListSeen(key SessionKey) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dirListSeen[key]
}

// CurrentCwd 返回该会话当前连接的产出区（无会话时返回 ""）。
// 区别于 Get：Get 总是回退到 worker 默认目录；切过 /dir 的会话要用实际 cwd。
func (m *SessionManager) CurrentCwd(key SessionKey) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.sessions[key]; ok {
		return c.Cwd()
	}
	return ""
}

// GetWithCwd 获取或创建会话连接，使用显式 cwd（如 web 项目真实路径）。
func (m *SessionManager) GetWithCwd(key SessionKey, cwd string) (*CataConn, error) {
	return m.GetWithCwdDialer(key, cwd, nil)
}

// dialerIdentity 归一化拨号标识：nil = 默认 socket（""）；非 nil = 自定义拨号（"dialer"）。
func dialerIdentity(d func() (net.Conn, error)) string {
	if d != nil {
		return "dialer"
	}
	return ""
}

// GetWithCwdDialer 获取或创建会话连接；dialer 非 nil 时该连接用自定义拨号
// （remote 模式按项目路由到对应在线 agent，本地按绑定 agent 拨其 per-ws socket），
// 否则走默认 connFactory。
// 缓存的连接若已失效（隧道抖动/断线后 socketclient 已标记失效），自动重建。
// 关键：**dialer 变化也重建**——绑定/换绑后必须拨到新 agent 的 socket，
// 不能复用旧的（否则换绑后消息仍发到旧工作空间）。
func (m *SessionManager) GetWithCwdDialer(key SessionKey, cwd string, dialer func() (net.Conn, error)) (*CataConn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dialerKey := dialerIdentity(dialer)
	if c, ok := m.sessions[key]; ok {
		if c.Cwd() == cwd && c.DialKey() == dialerKey {
			if !c.Healthy() {
				log.Printf("session %s: cached conn unhealthy, rebuilding", key)
				_ = c.Close()
				delete(m.sessions, key)
			} else {
				return c, nil
			}
		} else {
			// cwd 或 dialer 变化：旧连接不再匹配（如换绑 agent），必须重建。
			log.Printf("session %s: conn demux changed (cwd %q dialer %q -> cwd %q dialer %q), rebuilding",
				key, c.Cwd(), c.DialKey(), cwd, dialerKey)
			_ = c.Close()
			delete(m.sessions, key)
		}
	}
	var c *CataConn
	if dialer != nil {
		c = NewCataConnWithDialer("", cwd, dialer)
	} else {
		c = m.connFactory(m.socketPath, cwd)
	}
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
