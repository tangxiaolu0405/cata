package tunnel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMachinesStoreIssueValidate 验证逐机器 token 的签发与校验：明文签发、hash 存储、
// 正确 token 通过、错误 token 拒绝、跨机器不串。
func TestMachinesStoreIssueValidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machines.json")
	s := NewMachinesStore(path)

	tok, err := s.IssueToken("machine-a")
	if err != nil {
		t.Fatal(err)
	}
	if tok == "" {
		t.Fatal("issued token empty")
	}

	if !s.ValidateMachine("machine-a", tok) {
		t.Fatal("valid token should pass")
	}
	if s.ValidateMachine("machine-a", "wrong-token") {
		t.Fatal("wrong token should fail")
	}
	if s.ValidateMachine("machine-b", tok) {
		t.Fatal("token of machine-a should not validate as machine-b")
	}

	// 持久化后重新加载，token 仍有效。
	s2 := NewMachinesStore(path)
	if !s2.ValidateMachine("machine-a", tok) {
		t.Fatal("token should survive reload")
	}

	// hash 不落明文：文件里不应出现 token 原文。
	data, _ := os.ReadFile(path)
	if len(data) > 0 && strings.Contains(string(data), tok) {
		t.Fatal("machine token plaintext must not be persisted")
	}
}

// TestMachinesStoreRevoke 验证单机吊销：删一台不影响其它。
func TestMachinesStoreRevoke(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machines.json")
	s := NewMachinesStore(path)

	tokA, _ := s.IssueToken("machine-a")
	tokB, _ := s.IssueToken("machine-b")

	if err := s.RevokeMachine("machine-a"); err != nil {
		t.Fatal(err)
	}
	if s.ValidateMachine("machine-a", tokA) {
		t.Fatal("revoked machine token should fail")
	}
	if !s.ValidateMachine("machine-b", tokB) {
		t.Fatal("other machine token should still pass after revoke")
	}
}

// TestJoinManagerLifecycle 验证 join code 状态机：request → approve → status 领取，code 一次性。
func TestJoinManagerLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machines.json")
	j := NewJoinManager(NewMachinesStore(path))

	code, err := j.RequestJoin("machine-x")
	if err != nil {
		t.Fatal(err)
	}
	if code == "" {
		t.Fatal("empty join code")
	}

	// 批准前 status 返回未批准。
	st, err := j.Status(code)
	if err != nil {
		t.Fatal(err)
	}
	if st.Approved {
		t.Fatal("should not be approved before ApproveJoin")
	}

	// 批准。
	machineID, err := j.ApproveJoin(code)
	if err != nil {
		t.Fatal(err)
	}
	if machineID != "machine-x" {
		t.Fatalf("machineID=%q want machine-x", machineID)
	}

	// 批准后 status 返回 token。
	st, err = j.Status(code)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Approved || st.Token == "" {
		t.Fatalf("after approve: %+v err=%v", st, err)
	}
	token := st.Token

	// 该 token 应能通过 machines store 校验。
	if !j.store.ValidateMachine("machine-x", token) {
		t.Fatal("approved token should validate")
	}
}

// TestAgentTokenIssueValidateRevoke 验证 per-agent token：首次签发下发明文、重复签发
// 幂等（已存在不覆盖）、校验、跨 agent 不串、吊销单 agent 不影响同机其它。
func TestAgentTokenIssueValidateRevoke(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machines.json")
	s := NewMachinesStore(path)
	if _, err := s.IssueToken("machine-a"); err != nil {
		t.Fatal(err)
	}

	tok, exists, err := s.IssueAgentToken("ws-a", "machine-a")
	if err != nil || exists || tok == "" {
		t.Fatalf("first issue: tok=%q exists=%v err=%v", tok, exists, err)
	}

	// 已存在 → 不覆盖、不新发。
	_, exists2, err := s.IssueAgentToken("ws-a", "machine-a")
	if err != nil || !exists2 {
		t.Fatalf("second issue: exists=%v err=%v", exists2, err)
	}

	if !s.ValidateAgent("ws-a", tok) {
		t.Fatal("valid agent token should pass")
	}
	if s.ValidateAgent("ws-a", "wrong") {
		t.Fatal("wrong agent token should fail")
	}
	// 同机另一个 agent 未签发 → 校验失败。
	if s.ValidateAgent("ws-b", tok) {
		t.Fatal("token of ws-a must not validate as ws-b")
	}
	// 归属机器记录正确。
	if m := s.AgentMachine("ws-a"); m != "machine-a" {
		t.Fatalf("AgentMachine=%q want machine-a", m)
	}

	// 吊销 ws-a：自己失效，同机 ws-b（若有）不受影响。
	tokB, _, _ := s.IssueAgentToken("ws-b", "machine-a")
	if err := s.RevokeAgent("ws-a"); err != nil {
		t.Fatal(err)
	}
	if s.ValidateAgent("ws-a", tok) {
		t.Fatal("revoked agent token should fail")
	}
	if !s.ValidateAgent("ws-b", tokB) {
		t.Fatal("other agent token should still pass")
	}

	// 持久化后 reload 仍有效。
	s2 := NewMachinesStore(path)
	if !s2.ValidateAgent("ws-b", tokB) {
		t.Fatal("agent token should survive reload")
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), tokB) {
		t.Fatal("agent token plaintext must not be persisted")
	}
}
