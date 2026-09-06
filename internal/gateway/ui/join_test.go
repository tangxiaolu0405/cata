package ui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"cata/internal/gateway/tunnel"
)

// TestHandleJoinApprove 验证 UI 端口批准机器接入：进程内调 JoinManager，免 gateway_token。
func TestHandleJoinApprove(t *testing.T) {
	store := tunnel.NewMachinesStore(filepath.Join(t.TempDir(), "machines.json"))
	join := tunnel.NewJoinManager(store)
	code, err := join.RequestJoin("machine-x")
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{join: join}

	body := bytes.NewReader([]byte(`{"code":"` + code + `"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/join/approve", body)
	rec := httptest.NewRecorder()
	s.handleJoinApprove(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	// 批准后 Status 应返回 token 且能通过 store 校验。
	st, err := join.Status(code)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Approved || st.Token == "" {
		t.Fatalf("after approve, status should return approved token: %+v", st)
	}
	if !store.ValidateMachine("machine-x", st.Token) {
		t.Fatal("approved machine token should validate")
	}
}

// TestHandleJoinApproveRejectsNoJoin 无 join（本地模式）时拒绝。
func TestHandleJoinApproveRejectsNoJoin(t *testing.T) {
	s := &Server{join: nil}
	req := httptest.NewRequest(http.MethodPost, "/api/join/approve", bytes.NewReader([]byte(`{"code":"x"}`)))
	rec := httptest.NewRecorder()
	s.handleJoinApprove(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
}

// TestHandleAgentRevoke 验证 /api/agents/:id/revoke：吊销 per-agent token、
// 连接被断开、无效方法/空 store 拒绝。
func TestHandleAgentRevoke(t *testing.T) {
	machinePath := filepath.Join(t.TempDir(), "machines.json")
	store := tunnel.NewMachinesStore(machinePath)
	if _, err := store.IssueToken("machine-1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.IssueAgentToken("ws-a", "machine-1"); err != nil {
		t.Fatal(err)
	}

	// 在线注册表注入该 agent（同包测试可访问）。
	reg := tunnel.NewRegistry()
	if err := reg.RegisterAgent("ws-a", "machine-1"); err != nil {
		t.Fatal(err)
	}

	s := &Server{store: store, reg: reg}

	// 吊销成功。
	req := httptest.NewRequest(http.MethodPost, "/api/agents/ws-a/revoke", nil)
	rec := httptest.NewRecorder()
	s.handleAgentAction(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.HasAgentToken("ws-a") {
		t.Fatal("agent token should be revoked")
	}
	if reg.AgentAlive("ws-a") {
		t.Fatal("revoked agent should be disconnected")
	}

	// 再次吊销（已不存在）→ 400。
	req2 := httptest.NewRequest(http.MethodPost, "/api/agents/ws-a/revoke", nil)
	rec2 := httptest.NewRecorder()
	s.handleAgentAction(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("re-revoke status=%d want 400", rec2.Code)
	}

	// 无 store → 400。
	s2 := &Server{reg: tunnel.NewRegistry()}
	req3 := httptest.NewRequest(http.MethodPost, "/api/agents/ws-a/revoke", nil)
	rec3 := httptest.NewRecorder()
	s2.handleAgentAction(rec3, req3)
	if rec3.Code != http.StatusBadRequest {
		t.Fatalf("no-store status=%d want 400", rec3.Code)
	}
}
