package tunnel

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestJoinManager(t *testing.T) *JoinManager {
	t.Helper()
	store := NewMachinesStore(filepath.Join(t.TempDir(), "machines.json"))
	return NewJoinManager(store)
}

func TestJoinFlow(t *testing.T) {
	j := newTestJoinManager(t)
	code, err := j.RequestJoin("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if code == "" {
		t.Fatal("empty code")
	}
	// 未批准：pending。
	approved, token, err := j.Status(code)
	if err != nil {
		t.Fatal(err)
	}
	if approved || token != "" {
		t.Fatalf("expected pending, got approved=%v token=%q", approved, token)
	}
	// 批准：签发逐机器 token。
	machineID, err := j.ApproveJoin(code)
	if err != nil {
		t.Fatal(err)
	}
	if machineID != "machine-1" {
		t.Fatalf("machineID = %q, want machine-1", machineID)
	}
	// 批准后：approved + token 可领取。
	approved, token, err = j.Status(code)
	if err != nil {
		t.Fatal(err)
	}
	if !approved || token == "" {
		t.Fatalf("expected approved with token, got %v %q", approved, token)
	}
	// token 可校验（machines store 存 hash）。
	if !j.store.ValidateMachine("machine-1", token) {
		t.Fatal("issued token should validate")
	}
}

func TestApproveJoinIdempotent(t *testing.T) {
	j := newTestJoinManager(t)
	code, _ := j.RequestJoin("machine-1")
	machineID, err := j.ApproveJoin(code)
	if err != nil {
		t.Fatal(err)
	}
	// 重复批准同一 code：幂等，返回同一 machineID，不重复签发。
	machineID2, err := j.ApproveJoin(code)
	if err != nil {
		t.Fatal(err)
	}
	if machineID2 != machineID {
		t.Fatalf("idempotent approve mismatch: %q vs %q", machineID2, machineID)
	}
}

func TestJoinErrors(t *testing.T) {
	j := newTestJoinManager(t)
	if _, err := j.RequestJoin("  "); err == nil {
		t.Fatal("empty machine_id should error")
	}
	if _, _, err := j.Status("nonexistent"); err == nil {
		t.Fatal("invalid code should error on status")
	}
	if _, err := j.ApproveJoin("nonexistent"); err == nil {
		t.Fatal("invalid code should error on approve")
	}
}

func TestJoinExpired(t *testing.T) {
	j := newTestJoinManager(t)
	code, _ := j.RequestJoin("machine-1")
	// 同包测试：直接把 ExpiresAt 拨到过去，模拟过期。
	j.mu.Lock()
	j.pending[code].ExpiresAt = time.Now().Add(-time.Minute)
	j.mu.Unlock()

	if _, err := j.ApproveJoin(code); err == nil {
		t.Fatal("expired code should error on approve")
	}
	if _, _, err := j.Status(code); err == nil {
		t.Fatal("expired code should error on status")
	}
	// 过期条目应被清除。
	j.mu.Lock()
	_, stillPending := j.pending[code]
	j.mu.Unlock()
	if stillPending {
		t.Fatal("expired entry should be purged")
	}
}
