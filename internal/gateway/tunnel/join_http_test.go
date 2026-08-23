package tunnel

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestJoinProtoGate 验证 join 端点最外层 X-Cata-Join 协议头拦截：
// 未携带自定义头的请求（扫描器/爆破器特征）直接 400，不进入状态机；携带正确头的放行。
func TestJoinProtoGate(t *testing.T) {
	j := NewJoinManager(NewMachinesStore(t.TempDir()))
	handler := gateJoinProto(rateLimitJoin(nil, JoinRequestHandler(j)))

	// 无协议头 → 400。
	req := httptest.NewRequest(http.MethodPost, "/cata/v1/join/request",
		bytes.NewReader([]byte(`{"machine_id":"m1"}`)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no proto header: want 400, got %d", rec.Code)
	}

	// 错误协议头 → 400。
	req2 := httptest.NewRequest(http.MethodPost, "/cata/v1/join/request",
		bytes.NewReader([]byte(`{"machine_id":"m1"}`)))
	req2.Header.Set(JoinProtoHeaderName, "random-scan")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("bad proto header: want 400, got %d", rec2.Code)
	}

	// 正确协议头 → 200 且返回 join_code。
	req3 := httptest.NewRequest(http.MethodPost, "/cata/v1/join/request",
		bytes.NewReader([]byte(`{"machine_id":"m1"}`)))
	req3.Header.Set(JoinProtoHeaderName, JoinProtoHeaderValue)
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("valid proto header: want 200, got %d", rec3.Code)
	}
	if !bytes.Contains(rec3.Body.Bytes(), []byte("join_code")) {
		t.Fatalf("expected join_code in body, got %s", rec3.Body.String())
	}
}
