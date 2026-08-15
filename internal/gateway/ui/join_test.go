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
	_, token, err := join.Status(code)
	if err != nil || token == "" {
		t.Fatalf("after approve, status should return token: %v", err)
	}
	if !store.ValidateMachine("machine-x", token) {
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
