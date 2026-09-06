package gateway

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cata/internal/cata/config"
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
	m := NewRemoteSessionManager("/tmp/w", nil)

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
	_, _ = c1.Chat(context.Background(), "hi", nil)
	if len(dialed) != 1 || dialed[0].id != "ws-a" {
		t.Fatalf("dialed=%+v want exactly [ws-a]", dialed)
	}

	// 读失败后连接被 invalidate（EOF → unhealthy），下次 Get 应自动重建并重新 dial。
	c2, _ := m.GetWithCwdDialer("web:ws-a", "/proj/a", dialerFor("ws-a"))
	if c2 == c1 {
		t.Fatalf("after EOF the cached conn should be invalidated and rebuilt")
	}
	_, _ = c2.Chat(context.Background(), "hi", nil)
	if len(dialed) != 2 || dialed[1].id != "ws-a" {
		t.Fatalf("dialed=%+v want [ws-a ws-a] (rebuild redials same agent)", dialed)
	}

	// 不同键独立，各自 dial 自己的 agent。
	cb, _ := m.GetWithCwdDialer("web:ws-b", "/proj/b", dialerFor("ws-b"))
	_, _ = cb.Chat(context.Background(), "hi", nil)
	if len(dialed) != 3 || dialed[2].id != "ws-b" {
		t.Fatalf("dialed=%+v want [ws-a ws-a ws-b]", dialed)
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

// TestGetReusesSwitchedCwd 回归：/dir 切换后，逐条消息调用的 Get(key)
// 必须复用切换后的连接（产出区=新路径），而不是回退到默认 worker 目录
// （否则"切完再发消息还是原路径"）。
func TestGetReusesSwitchedCwd(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "cata-gwtest-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	t.Setenv(config.EnvCataHome, home)
	t.Setenv(config.EnvConfigFile, "")
	root := filepath.Join(home, "w")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0755); err != nil {
		t.Fatal(err)
	}
	stubAgentSocket(t, proj)
	m := NewSessionManager(filepath.Join(root, "cata.sock"), root)
	key := SessionKeyFor("test", "5")

	c1, err := m.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if reply, _ := HandleWorkdirCommand(m, key, proj); !strings.Contains(reply, "产出区已切换") {
		t.Fatalf("切换失败：%q", reply)
	}
	// 消息处理路径：Get(key) 必须拿到切换后的连接（cwd=proj）。
	c2, err := m.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Cwd() != proj {
		t.Fatalf("Get 复用了错误连接 cwd=%q want %q（切换被打回原路径）", c2.Cwd(), proj)
	}
	_ = c1
}
