package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
)

// newLockTestHome 用临时 CATA_HOME 隔离锁目录。
func newLockTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)
	if err := brain.EnsureCataLayout(); err != nil {
		t.Fatal(err)
	}
	return home
}

// TestOutputLockBusyWhenHeldByLiveProcess 被存活进程持有时报 busy，并给出持有者信息与
// 可操作提示（pid / 开始时间 / 锁文件路径）——这正是 `cata` 之前报的那条错误。
func TestOutputLockBusyWhenHeldByLiveProcess(t *testing.T) {
	home := newLockTestHome(t)
	dir := filepath.Join(home, "proj")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	release, err := AcquireOutputLock(dir)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer release()

	// 同进程再取一次：当前实现按「pid 存活」判定 busy（TUI/网关互斥就靠它）。
	if _, err := AcquireOutputLock(dir); err == nil {
		t.Fatal("second acquire should fail while held")
	} else {
		msg := err.Error()
		for _, want := range []string{"output area busy", dir, "pid ", "out_", ".lock"} {
			if !strings.Contains(msg, want) {
				t.Fatalf("busy error %q missing %q", msg, want)
			}
		}
	}

	// 释放后可再取。
	release()
	if _, err := AcquireOutputLock(dir); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
}

// TestOutputLockReclaimsDeadHolder 持有者进程已死（崩溃 / kill -9）→ 自动回收，不需手工删文件。
func TestOutputLockReclaimsDeadHolder(t *testing.T) {
	home := newLockTestHome(t)
	dir := filepath.Join(home, "proj2")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path, err := outputLockPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 伪造一个「pid 早已不存在」的锁文件（用不可能的 pid）。
	if err := os.WriteFile(path, []byte(`{"pid":999999,"output_cwd":"`+dir+`","started_at":"2020-01-01T00:00:00+08:00"}`), 0644); err != nil {
		t.Fatal(err)
	}
	release, err := AcquireOutputLock(dir)
	if err != nil {
		t.Fatalf("stale lock should be reclaimed: %v", err)
	}
	release()
}

// TestPruneStaleOutputLocks 只清理死持有者的锁文件，存活者的（含损坏文件）不动/清理行为明确。
func TestPruneStaleOutputLocks(t *testing.T) {
	home := newLockTestHome(t)
	locksDir := filepath.Join(home, "locks")

	liveDir := filepath.Join(home, "live")
	deadDir := filepath.Join(home, "dead")
	for _, d := range []string{liveDir, deadDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	release, err := AcquireOutputLock(liveDir) // 本进程持有
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	deadPath, err := outputLockPath(deadDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deadPath, []byte(`{"pid":999998}`), 0644); err != nil {
		t.Fatal(err)
	}
	// 无关文件不该被碰。
	junk := filepath.Join(locksDir, "ws-abc.lock")
	if err := os.WriteFile(junk, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	if n := PruneStaleOutputLocks(); n != 1 {
		t.Fatalf("pruned=%d want 1", n)
	}
	if _, err := os.Stat(deadPath); !os.IsNotExist(err) {
		t.Fatal("dead-holder lock should be removed")
	}
	livePath, _ := outputLockPath(liveDir)
	if _, err := os.Stat(livePath); err != nil {
		t.Fatalf("live-holder lock must be kept: %v", err)
	}
	if _, err := os.Stat(junk); err != nil {
		t.Fatalf("unrelated file must be kept: %v", err)
	}
}
