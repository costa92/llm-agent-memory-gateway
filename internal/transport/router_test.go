package transport

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	"github.com/costa92/llm-agent-memory-gateway/internal/httpapi"
)

type stubService struct{}

func (stubService) RecallUnified(context.Context, authz.Scope, httpapi.RecallUnifiedRequest) (httpapi.RecallUnifiedResponse, error) {
	return httpapi.RecallUnifiedResponse{}, nil
}
func (stubService) WriteMemory(context.Context, authz.Scope, httpapi.WriteMemoryRequest) (httpapi.WriteMemoryResponse, error) {
	return httpapi.WriteMemoryResponse{}, nil
}
func (stubService) PatchMemory(context.Context, authz.Scope, string, httpapi.PatchMemoryRequest) (httpapi.PatchMemoryResponse, error) {
	return httpapi.PatchMemoryResponse{}, nil
}
func (stubService) PinMemory(context.Context, authz.Scope, string, httpapi.PinMemoryRequest) (httpapi.PinMemoryResponse, error) {
	return httpapi.PinMemoryResponse{}, nil
}
func (stubService) UnpinMemory(context.Context, authz.Scope, string, httpapi.PinMemoryRequest) (httpapi.PinMemoryResponse, error) {
	return httpapi.PinMemoryResponse{}, nil
}
func (stubService) DisableMemory(context.Context, authz.Scope, string, httpapi.DisableMemoryRequest) (httpapi.DisableMemoryResponse, error) {
	return httpapi.DisableMemoryResponse{}, nil
}
func (stubService) EnableMemory(context.Context, authz.Scope, string, httpapi.DisableMemoryRequest) (httpapi.DisableMemoryResponse, error) {
	return httpapi.DisableMemoryResponse{}, nil
}
func (stubService) DeleteMemory(context.Context, authz.Scope, string, httpapi.DeleteMemoryRequest) (httpapi.DeleteMemoryResponse, error) {
	return httpapi.DeleteMemoryResponse{}, nil
}
func (stubService) CloseSession(context.Context, authz.Scope, string, httpapi.SessionCloseRequest) (httpapi.SessionCloseResponse, error) {
	return httpapi.SessionCloseResponse{}, nil
}
func (stubService) HeartbeatSession(context.Context, authz.Scope, string, httpapi.SessionHeartbeatRequest) (httpapi.SessionHeartbeatResponse, error) {
	return httpapi.SessionHeartbeatResponse{}, nil
}
func (stubService) GetMemoryItem(context.Context, authz.Scope, string) (httpapi.GetMemoryItemResponse, error) {
	return httpapi.GetMemoryItemResponse{}, nil
}

func TestRoutes_AreRegistered(t *testing.T) {
	handler := NewHandler(stubService{}, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	})

	testCases := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPost, path: "/memory/recall/unified", body: `{"scope":{"tenant_id":"tenant-a","user_id":"user-1"},"query":"pdf"}`},
		{method: http.MethodPost, path: "/memory/write", body: `{"idempotency_key":"idem_1","scope":{"tenant_id":"tenant-a","user_id":"user-1"},"record":{"kind":"semantic","source":"user_saved","category":"project","content":"remember"}}`},
		{method: http.MethodPatch, path: "/memory/items/mem_123", body: `{"scope":{"tenant_id":"tenant-a","user_id":"user-1"},"expected_version":1,"patch":{"content":"updated"}}`},
		{method: http.MethodPost, path: "/memory/items/mem_123/pin", body: `{"scope":{"tenant_id":"tenant-a","user_id":"user-1"},"expected_version":1}`},
		{method: http.MethodPost, path: "/memory/items/mem_123/unpin", body: `{"scope":{"tenant_id":"tenant-a","user_id":"user-1"},"expected_version":1}`},
		{method: http.MethodPost, path: "/memory/items/mem_123/disable", body: `{"scope":{"tenant_id":"tenant-a","user_id":"user-1"},"expected_version":1}`},
		{method: http.MethodPost, path: "/memory/items/mem_123/enable", body: `{"scope":{"tenant_id":"tenant-a","user_id":"user-1"},"expected_version":1}`},
		{method: http.MethodDelete, path: "/memory/items/mem_123", body: `{"scope":{"tenant_id":"tenant-a","user_id":"user-1"},"expected_version":1,"consistency_level":"strong"}`},
		{method: http.MethodGet, path: "/memory/items/mem_123", body: ``},
		{method: http.MethodPost, path: "/memory/sessions/sess_9/close", body: `{"scope":{"tenant_id":"tenant-a","user_id":"user-1","session_id":"sess_9"},"mode":"expire_working"}`},
		{method: http.MethodPost, path: "/memory/sessions/sess_9/heartbeat", body: `{"scope":{"tenant_id":"tenant-a","user_id":"user-1","session_id":"sess_9"}}`},
		{method: http.MethodGet, path: "/metrics", body: ``},
	}

	for _, tc := range testCases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Tenant-Id", "tenant-a")
			req.Header.Set("X-User-Id", "user-1")
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, req)

			if recorder.Code == http.StatusNotFound {
				t.Fatalf("status = %d, want non-404", recorder.Code)
			}
		})
	}
}

func TestRoute_MethodConstraint(t *testing.T) {
	handler := NewHandler(stubService{})
	req := httptest.NewRequest(http.MethodGet, "/memory/write", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}
