package scheduler

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cata/internal/cata/config"
)

// shortSchedulerHome 用短路径 CATA_HOME（Unix socket 路径有 sun_path 104 字节上限，
// t.TempDir() 的路径过长会导致 bind: invalid argument）。
func shortSchedulerHome(t *testing.T) {
	t.Helper()
	home := filepath.Join(os.TempDir(), fmt.Sprintf("cata-sched-%d", time.Now().UnixNano()%100000))
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvCataHome, home)
	t.Cleanup(func() { _ = os.RemoveAll(home) })
}

func TestDaemonAliveAndAcquireLock(t *testing.T) {
	shortSchedulerHome(t)
	if DaemonAlive() {
		t.Fatal("no daemon should be alive on empty home")
	}

	ln, acquired, err := AcquireDaemonLock()
	if err != nil || !acquired {
		t.Fatalf("first acquire = (%v, %v, %v), want acquired", ln != nil, acquired, err)
	}
	if !DaemonAlive() {
		t.Fatal("DaemonAlive should be true while lock held")
	}

	// 已有守护 → 第二次获取失败。
	ln2, acquired2, err := AcquireDaemonLock()
	if err != nil || acquired2 {
		t.Fatalf("second acquire should fail: (%v, %v, %v)", ln2 != nil, acquired2, err)
	}

	// 释放（Close + Remove）后再次可获取。
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(DaemonSocketPath())
	ln3, acquired3, err := AcquireDaemonLock()
	if err != nil || !acquired3 {
		t.Fatalf("acquire after release = (%v, %v, %v)", ln3 != nil, acquired3, err)
	}
	ln3.Close()
	_ = os.Remove(DaemonSocketPath())
}

func TestAcquireDaemonLockCleansStaleSocket(t *testing.T) {
	shortSchedulerHome(t)
	// 伪造 stale socket 文件（无监听进程）。
	if err := os.MkdirAll(Dir(), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(DaemonSocketPath(), []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}
	if DaemonAlive() {
		t.Fatal("stale socket file should not report alive")
	}
	ln, acquired, err := AcquireDaemonLock()
	if err != nil || !acquired {
		t.Fatalf("acquire over stale socket = (%v, %v, %v), want acquired", ln != nil, acquired, err)
	}
	ln.Close()
	_ = os.Remove(DaemonSocketPath())
}

func TestEnsureDaemonRunning(t *testing.T) {
	shortSchedulerHome(t)
	old := daemonCommand
	defer func() { daemonCommand = old }()

	// spawn 成功路径：用 sleep 顶替 cata schedule（避免测试递归）。
	daemonCommand = func() (string, []string) { return "sleep", []string{"1"} }
	spawned, err := EnsureDaemonRunning()
	if err != nil {
		t.Fatalf("EnsureDaemonRunning: %v", err)
	}
	if !spawned {
		t.Fatal("should have spawned daemon")
	}

	// 已有守护（socket 可连接）→ no-op。
	ln, _, err := AcquireDaemonLock()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { ln.Close(); _ = os.Remove(DaemonSocketPath()) }()
	spawned2, err := EnsureDaemonRunning()
	if err != nil || spawned2 {
		t.Fatalf("EnsureDaemonRunning with live daemon = (%v, %v), want (false, nil)", spawned2, err)
	}

	// spawn 失败路径：可执行文件不存在。
	ln.Close()
	_ = os.Remove(DaemonSocketPath())
	daemonCommand = func() (string, []string) { return "/nonexistent/cata", []string{"schedule"} }
	if _, err := EnsureDaemonRunning(); err == nil {
		t.Fatal("expected error spawning nonexistent binary")
	}
}
