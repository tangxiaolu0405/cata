package link

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
)

// TestStopAllAgentsNoAgents supervisor 无注册 agent 时 stopAllAgents 应无副作用、不 panic。
func TestStopAllAgentsNoAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	if err := brain.EnsureCataLayout(); err != nil {
		t.Fatal(err)
	}
	// 清空 link.json（此时不存在 → LoadConfig 返回空）。
	s := &Supervisor{}
	s.stopAllAgents() // 不应 panic / 报错
	if _, err := os.Stat(config.LinkConfigPath()); err != nil && !os.IsNotExist(err) {
		t.Fatalf("link.json should not be created by stopAllAgents: %v", err)
	}
}

// TestStopAllAgentsStopsRegistered 有注册 agent（但 pid 文件不存在）时，stopAllAgents
// 对每个 id 尝试停止并跳过「未运行」，不中断其它项。
func TestStopAllAgentsStopsRegistered(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)
	if err := brain.EnsureCataLayout(); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		GatewayURL:   "http://127.0.0.1:8787",
		MachineToken: "m-tok",
		Agents: map[string]AgentEntry{
			"ws-a": {AgentID: "ws-a", RootPath: filepath.Join(home, "a"), Enabled: true, KeepAlive: true},
			"ws-b": {AgentID: "ws-b", RootPath: filepath.Join(home, "b"), Enabled: true, KeepAlive: true},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	// pid 文件不存在 → 各 agent 视为未运行，stopAllAgents 应逐个跳过并返回。
	s := &Supervisor{}
	s.stopAllAgents() // 不应 panic
}

// TestWatchSupervisorStopsOnUnreachable kill supervisor（含 SIGKILL）后 agent 收不到信号，
// WatchSupervisorAndStop 应在失联超过 Deadline 后调用 stop()，让 agent 优雅退出。
func TestWatchSupervisorStopsOnUnreachable(t *testing.T) {
	stopped := make(chan struct{}, 1)
	alive := func() bool { return false } // supervisor 已死

	watch := WatchSupervisorAndStop(SupervisorWatchConfig{
		Interval: 20 * time.Millisecond,
		Deadline: 60 * time.Millisecond,
		AliveFn:  alive,
	}, func() {
		select {
		case stopped <- struct{}{}:
		default:
		}
	})
	go watch()

	select {
	case <-stopped:
		// ok：失联超时后 stop 被调用
	case <-time.After(2 * time.Second):
		t.Fatal("stop was not called after supervisor became unreachable")
	}
}

// TestWatchSupervisorKeepsAlive supervisor 存活时 watch 不应触发 stop。
func TestWatchSupervisorKeepsAlive(t *testing.T) {
	stopped := make(chan struct{}, 1)
	alive := func() bool { return true }

	watch := WatchSupervisorAndStop(SupervisorWatchConfig{
		Interval: 20 * time.Millisecond,
		Deadline: 60 * time.Millisecond,
		AliveFn:  alive,
	}, func() {
		select {
		case stopped <- struct{}{}:
		default:
		}
	})
	done := make(chan struct{})
	go func() { watch(); close(done) }()

	time.Sleep(150 * time.Millisecond)
	select {
	case <-stopped:
		t.Fatal("stop called while supervisor alive")
	default:
	}
}

// TestWatchSupervisorBusyDefersShutdown 失联超时但仍有活跃会话（Busy=true）时，
// 不应立即 stop（EOF 根因回归）；空闲后才 stop。
func TestWatchSupervisorBusyDefersShutdown(t *testing.T) {
	stopped := make(chan struct{}, 1)
	busy := true // 先 busy
	watch := WatchSupervisorAndStop(SupervisorWatchConfig{
		Interval: 20 * time.Millisecond,
		Deadline: 40 * time.Millisecond,
		AliveFn:  func() bool { return false }, // supervisor 死了
		Busy:     func() bool { return busy },
	}, func() {
		select {
		case stopped <- struct{}{}:
		default:
		}
	})
	go watch()

	// busy 期间（持续 200ms > deadline）不应 stop。
	time.Sleep(150 * time.Millisecond)
	select {
	case <-stopped:
		t.Fatal("stop called while busy chat session active")
	default:
	}

	// 转为空闲后应在一次 deadline 内 stop。
	busy = false
	select {
	case <-stopped:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("stop not called after becoming idle")
	}
}

// TestSupervisorWatchEnvOverrides 环境变量 CATA_SUPERVISOR_INTERVAL/DEADLINE 应覆盖默认值。
func TestSupervisorWatchEnvOverrides(t *testing.T) {
	t.Setenv("CATA_SUPERVISOR_INTERVAL", "2")
	t.Setenv("CATA_SUPERVISOR_DEADLINE", "7")
	i, d := supervisorWatchDefaults()
	if i != 2*time.Second || d != 7*time.Second {
		t.Fatalf("interval=%v deadline=%v", i, d)
	}
}

// TestSupervisorWatchEnvInvalidConfigFallback 非法/非正环境变量应回落代码默认。
func TestSupervisorWatchEnvInvalidConfigFallback(t *testing.T) {
	t.Setenv("CATA_SUPERVISOR_INTERVAL", "abc")
	t.Setenv("CATA_SUPERVISOR_DEADLINE", "-3")
	i, d := supervisorWatchDefaults()
	if i != 5*time.Second || d != 30*time.Second {
		t.Fatalf("interval=%v deadline=%v", i, d)
	}
}

// TestSupervisorWatchEnvVsExplicitConfig 显式 cfg 优先于环境变量。
func TestSupervisorWatchEnvVsExplicitConfig(t *testing.T) {
	t.Setenv("CATA_SUPERVISOR_DEADLINE", "7")
	stopped := make(chan struct{}, 1)
	watch := WatchSupervisorAndStop(SupervisorWatchConfig{
		Interval: 15 * time.Millisecond,
		Deadline: 30 * time.Millisecond, // 显式传入，覆盖环境变量 7s
		AliveFn:  func() bool { return false },
	}, func() {
		select {
		case stopped <- struct{}{}:
		default:
		}
	})
	go watch()
	select {
	case <-stopped:
		// ok：~30ms 内触发，说明用的是显式 Deadline 而非环境变量 7s
	case <-time.After(time.Second):
		t.Fatal("explicit deadline not respected")
	}
}
