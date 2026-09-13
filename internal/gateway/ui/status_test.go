package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cata/internal/gateway"
)

// TestHandleStatus 验证 /api/status 返回运行状态（会话数、provider/model、idle 配置）。
func TestHandleStatus(t *testing.T) {
	cfg := gateway.Config{
		Edition:                   "base",
		SessionIdleTimeoutSeconds: 3600,
	}
	s := &Server{cfg: cfg}

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rec := httptest.NewRecorder()
	s.handleStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true {
		t.Fatalf("ok = %v, want true", body["ok"])
	}
	if body["remote"] != false {
		t.Fatalf("remote = %v, want false", body["remote"])
	}
	if body["idle_timeout_s"] != float64(3600) {
		t.Fatalf("idle_timeout_s = %v, want 3600", body["idle_timeout_s"])
	}
	// web_sessions 应为 0（无 web 管理器）或数字。
	if _, ok := body["web_sessions"].(float64); !ok {
		t.Fatalf("web_sessions not a number: %v", body["web_sessions"])
	}
}

// TestHandleStatusMethodNotAllowed POST 拒绝。
func TestHandleStatusMethodNotAllowed(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/status", nil)
	rec := httptest.NewRecorder()
	s.handleStatus(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d want 405", rec.Code)
	}
}
