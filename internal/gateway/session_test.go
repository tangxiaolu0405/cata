package gateway

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestSessionManagerDialerRouting 验证 per-ws 路由的核心抽象：GetWithCwdDialer
// 用 dialer（而非 legacy connFactory）创建连接，且同一会话键缓存连接、不同键独立。
// 这是本地 per-ws 与 remote 隧道共用的会话层——dialer 决定「拨哪个 agent」。
func TestSessionManagerDialerRouting(t *testing.T) {
	type dialRecord struct{ id string }
	var dialed []dialRecord
	dialerFor := func(id string) func() (net.Conn, error) {
		return func() (net.Conn, error) {
			dialed = append(dialed, dialRecord{id: id})
			// 返回一个立即 EOF 的连接即可——本测试只关心 dialer 被按正确 id 调用。
			return &eofConn{}, nil
		}
	}

	// 无 connFactory 的 manager：dialer 必须被使用（否则 GetWithCwdDialer 会走 nil factory 报错）。
	m := NewRemoteSessionManagerWithStore("/tmp/w", nil, nil)

	c1, err := m.GetWithCwdDialer("web:ws-a", "/proj/a", dialerFor("ws-a"))
	if err != nil {
		t.Fatal(err)
	}
	if c1.Cwd() != "/proj/a" {
		t.Fatalf("cwd=%q want /proj/a", c1.Cwd())
	}
	if len(dialed) != 0 {
		t.Fatalf("dialer should be lazy (no dial until Chat), got %d", len(dialed))
	}

	// 触发实际拨号（Chat 会 ensureConn → dialFunc）。
	// eofConn 读即 EOF（死连接），socketclient 会自动失效并重拨一次后重发，
	// 故这里同一轮 chat 会拨两次（都指向同一 agent）——这正是「agent 空闲退出后
	// 缓存连接未失效」场景的自愈行为（见 TestChatRetriesOnDeadConn）。
	_, _ = c1.Chat(context.Background(), "hi", nil)
	if len(dialed) != 2 || dialed[0].id != "ws-a" || dialed[1].id != "ws-a" {
		t.Fatalf("dialed=%+v want [ws-a ws-a] (dead conn self-heal redials same agent)", dialed)
	}

	// 读失败后连接被 invalidate（EOF → unhealthy），下次 Get 应自动重建。
	c2, _ := m.GetWithCwdDialer("web:ws-a", "/proj/a", dialerFor("ws-a"))
	if c2 == c1 {
		t.Fatalf("after EOF the cached conn should be invalidated and rebuilt")
	}
	_, _ = c2.Chat(context.Background(), "hi", nil)
	if len(dialed) != 4 || dialed[2].id != "ws-a" || dialed[3].id != "ws-a" {
		t.Fatalf("dialed=%+v want [ws-a ws-a ws-a ws-a]", dialed)
	}

	// 不同键独立，各自 dial 自己的 agent。
	cb, _ := m.GetWithCwdDialer("web:ws-b", "/proj/b", dialerFor("ws-b"))
	_, _ = cb.Chat(context.Background(), "hi", nil)
	if len(dialed) != 6 || dialed[4].id != "ws-b" || dialed[5].id != "ws-b" {
		t.Fatalf("dialed=%+v want [ws-a ws-a ws-a ws-a ws-b ws-b]", dialed)
	}
}

// eofConn 是一个立即返回 EOF 的 net.Conn 假件（触发 socketclient invalidate）。
type eofConn struct{}

func (eofConn) Read([]byte) (int, error)         { return 0, net.ErrClosed }
func (eofConn) Write(p []byte) (int, error)      { return len(p), nil }
func (eofConn) Close() error                     { return nil }
func (eofConn) LocalAddr() net.Addr              { return dummyAddr{} }
func (eofConn) RemoteAddr() net.Addr             { return dummyAddr{} }
func (eofConn) SetDeadline(time.Time) error      { return nil }
func (eofConn) SetReadDeadline(time.Time) error  { return nil }
func (eofConn) SetWriteDeadline(time.Time) error { return nil }

type dummyAddr struct{}

func (dummyAddr) Network() string { return "dummy" }
func (dummyAddr) String() string  { return "dummy" }

// TestSessionManagerReapIdle 验证会话空闲回收：超过 idle 阈值的会话连接被关闭并删除。
func TestSessionManagerReapIdle(t *testing.T) {
	m := NewSessionManagerWithStore("", t.TempDir(), nil)
	m.SetIdleTimeout(50 * time.Millisecond)

	// 建立两个会话连接（eofConn 即可，不实际读写）。
	dial1 := func() (net.Conn, error) { return &eofConn{}, nil }
	dial2 := func() (net.Conn, error) { return &eofConn{}, nil }

	if _, err := m.GetWithCwdDialer("tg:1", "/tmp", dial1); err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetWithCwdDialer("tg:2", "/tmp", dial2); err != nil {
		t.Fatal(err)
	}
	if m.SessionCount() != 2 {
		t.Fatalf("session count: got %d want 2", m.SessionCount())
	}

	// 模拟 c1 被使用（刷新 lastUsed），c2 保持旧。
	time.Sleep(30 * time.Millisecond)
	if _, err := m.GetWithCwdDialer("tg:1", "/tmp", dial1); err != nil {
		t.Fatal(err)
	}

	// 再等 30ms：c2 超时（50ms），c1 未超时（30ms 前刚用）。
	time.Sleep(30 * time.Millisecond)
	m.reapIdle()

	if m.SessionCount() != 1 {
		t.Fatalf("after reap: session count = %d, want 1 (c2 reaped, c1 kept)", m.SessionCount())
	}
	if m.conn("tg:1") == nil {
		t.Fatal("tg:1 should still exist (recently used)")
	}
	if m.conn("tg:2") != nil {
		t.Fatal("tg:2 should be reaped")
	}
}

// TestSessionManagerReapDisabled 验证 idle timeout=0 时不回收。
func TestSessionManagerReapDisabled(t *testing.T) {
	m := NewSessionManagerWithStore("", t.TempDir(), nil)
	m.SetIdleTimeout(0) // 不回收

	dial := func() (net.Conn, error) { return &eofConn{}, nil }
	if _, err := m.GetWithCwdDialer("tg:1", "/tmp", dial); err != nil {
		t.Fatal(err)
	}
	m.reapIdle()
	if m.SessionCount() != 1 {
		t.Fatalf("idle timeout 0 should not reap, got %d", m.SessionCount())
	}
}

// conn 辅助：取内部连接（测试用）。
func (m *SessionManager) conn(key SessionKey) *CataConn {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[key]
}
