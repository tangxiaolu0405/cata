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
	st, err := j.Status(code)
	if err != nil {
		t.Fatal(err)
	}
	if st.Approved || st.Token != "" {
		t.Fatalf("expected pending, got %+v", st)
	}
	// 批准：签发逐机器 token。
	machineID, err := j.ApproveJoin(code)
	if err != nil {
		t.Fatal(err)
	}
	if machineID != "machine-1" {
		t.Fatalf("machineID = %q, want machine-1", machineID)
	}
	// 批准后：approved + token 可领取，且带批准时间。
	st, err = j.Status(code)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Approved || st.Token == "" {
		t.Fatalf("expected approved with token, got %+v", st)
	}
	if st.ApprovedAt.IsZero() {
		t.Fatal("approved_at should be set after approve")
	}
	// token 可校验（machines store 存 hash）。
	if !j.store.ValidateMachine("machine-1", st.Token) {
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
	if _, err := j.Status("nonexistent"); err == nil {
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
	if _, err := j.Status(code); err == nil {
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

// TestJoinPending 验证 Pending() 只返回未批准、未过期的请求，批准后不再出现。
func TestJoinPending(t *testing.T) {
	j := newTestJoinManager(t)
	code1, _ := j.RequestJoin("machine-1")
	code2, _ := j.RequestJoin("machine-2")

	pending := j.Pending()
	if len(pending) != 2 {
		t.Fatalf("want 2 pending, got %d: %+v", len(pending), pending)
	}
	byCode := map[string]string{}
	for _, p := range pending {
		byCode[p.Code] = p.MachineID
	}
	if byCode[code1] != "machine-1" || byCode[code2] != "machine-2" {
		t.Fatalf("pending mapping mismatch: %+v", byCode)
	}

	// 批准一台后不再出现在待批准列表。
	if _, err := j.ApproveJoin(code1); err != nil {
		t.Fatal(err)
	}
	pending = j.Pending()
	if len(pending) != 1 || pending[0].Code != code2 {
		t.Fatalf("want only code2 pending, got %+v", pending)
	}
}

// TestJoinChallenge 验证挑战-应答：签名伪造拒绝、一次性防重放、正确签名放行。
func TestJoinChallenge(t *testing.T) {
	j := newTestJoinManager(t)

	ch, sig, err := j.NewChallenge()
	if err != nil || ch == "" || sig == "" {
		t.Fatalf("new challenge: %v %q %q", err, ch, sig)
	}
	// 正确签名 + 首次使用 → 通过。
	if err := j.VerifyChallenge(ch, sig); err != nil {
		t.Fatalf("verify valid challenge: %v", err)
	}
	// 已使用（一次性）→ 拒绝。
	if err := j.VerifyChallenge(ch, sig); err == nil {
		t.Fatal("replayed challenge should fail")
	}
	// 伪造签名 → 拒绝。
	ch2, _, err := j.NewChallenge()
	if err != nil {
		t.Fatal(err)
	}
	if err := j.VerifyChallenge(ch2, "deadbeef"); err == nil {
		t.Fatal("forged signature should fail")
	}
}
