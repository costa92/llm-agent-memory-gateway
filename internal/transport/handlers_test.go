package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	"github.com/costa92/llm-agent-memory-gateway/internal/httpapi"
)

type captureService struct {
	lastAuthScope  authz.Scope
	lastWriteReq   httpapi.WriteMemoryRequest
	lastDeleteReq  httpapi.DeleteMemoryRequest
	lastRecallReq  httpapi.RecallUnifiedRequest
	lastPinReq     httpapi.PinMemoryRequest
	lastDisableReq httpapi.DisableMemoryRequest
}

func (s *captureService) RecallUnified(_ context.Context, authScope authz.Scope, req httpapi.RecallUnifiedRequest) (httpapi.RecallUnifiedResponse, error) {
	s.lastAuthScope = authScope
	s.lastRecallReq = req
	level := req.ConsistencyLevel
	if level == "" {
		level = "eventual"
	}
	return httpapi.RecallUnifiedResponse{
		Hits:  []httpapi.RecallHitResponse{{MemoryID: "mem_123", Kind: "semantic", Version: 1, Content: "remembered", Metadata: httpapi.RecallHitMetadata{TokenCostEstimate: 4}}},
		Trace: &httpapi.RecallTraceResponse{ConsistencyLevel: level},
	}, nil
}

func (s *captureService) WriteMemory(_ context.Context, authScope authz.Scope, req httpapi.WriteMemoryRequest) (httpapi.WriteMemoryResponse, error) {
	s.lastAuthScope = authScope
	s.lastWriteReq = req
	return httpapi.WriteMemoryResponse{
		Memory: httpapi.WriteMemoryResult{MemoryID: "mem_123", Version: 2, Status: "saved"},
	}, nil
}

func (s *captureService) PatchMemory(context.Context, authz.Scope, string, httpapi.PatchMemoryRequest) (httpapi.PatchMemoryResponse, error) {
	return httpapi.PatchMemoryResponse{MemoryID: "mem_123", Version: 3}, nil
}
func (s *captureService) PinMemory(_ context.Context, authScope authz.Scope, _ string, req httpapi.PinMemoryRequest) (httpapi.PinMemoryResponse, error) {
	s.lastAuthScope = authScope
	s.lastPinReq = req
	return httpapi.PinMemoryResponse{MemoryID: "mem_123", Version: 4, Pinned: true}, nil
}
func (s *captureService) UnpinMemory(_ context.Context, authScope authz.Scope, _ string, req httpapi.PinMemoryRequest) (httpapi.PinMemoryResponse, error) {
	s.lastAuthScope = authScope
	s.lastPinReq = req
	return httpapi.PinMemoryResponse{MemoryID: "mem_123", Version: 5, Pinned: false}, nil
}
func (s *captureService) DisableMemory(_ context.Context, authScope authz.Scope, _ string, req httpapi.DisableMemoryRequest) (httpapi.DisableMemoryResponse, error) {
	s.lastAuthScope = authScope
	s.lastDisableReq = req
	return httpapi.DisableMemoryResponse{MemoryID: "mem_123", Version: 5, Disabled: true}, nil
}
func (s *captureService) EnableMemory(_ context.Context, authScope authz.Scope, _ string, req httpapi.DisableMemoryRequest) (httpapi.DisableMemoryResponse, error) {
	s.lastAuthScope = authScope
	s.lastDisableReq = req
	return httpapi.DisableMemoryResponse{MemoryID: "mem_123", Version: 6, Disabled: false}, nil
}
func (s *captureService) DeleteMemory(_ context.Context, authScope authz.Scope, _ string, req httpapi.DeleteMemoryRequest) (httpapi.DeleteMemoryResponse, error) {
	s.lastAuthScope = authScope
	s.lastDeleteReq = req
	return httpapi.DeleteMemoryResponse{MemoryID: "mem_123", Deleted: true, Version: 6}, nil
}
func (s *captureService) CloseSession(context.Context, authz.Scope, string, httpapi.SessionCloseRequest) (httpapi.SessionCloseResponse, error) {
	return httpapi.SessionCloseResponse{SessionID: "sess_9", Status: "closed"}, nil
}
func (s *captureService) HeartbeatSession(context.Context, authz.Scope, string, httpapi.SessionHeartbeatRequest) (httpapi.SessionHeartbeatResponse, error) {
	return httpapi.SessionHeartbeatResponse{SessionID: "sess_9", Status: "active"}, nil
}
func (s *captureService) GetMemoryItem(_ context.Context, _ authz.Scope, memoryID string) (httpapi.GetMemoryItemResponse, error) {
	return httpapi.GetMemoryItemResponse{MemoryID: memoryID, Kind: "semantic", Version: 7, Content: "remembered"}, nil
}

func TestWriteHandler_MalformedJSON(t *testing.T) {
	handler := NewHandler(&captureService{})
	req := httptest.NewRequest(http.MethodPost, "/memory/write", bytes.NewBufferString(`{`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-Id", "tenant-a")
	req.Header.Set("X-User-Id", "user-1")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestWriteHandler_SetsVersionAndRequestIDHeaders(t *testing.T) {
	svc := &captureService{}
	handler := NewHandler(svc)

	body := `{"idempotency_key":"idem_1","scope":{"tenant_id":"tenant-claimed","user_id":"user-claimed"},"record":{"kind":"semantic","source":"user_saved","category":"project","content":"remember"}}`
	req := httptest.NewRequest(http.MethodPost, "/memory/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-Id", "tenant-auth")
	req.Header.Set("X-User-Id", "user-auth")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("X-Memory-Version"); got != "2" {
		t.Fatalf("X-Memory-Version = %q, want 2", got)
	}
	if recorder.Header().Get("X-Request-Id") == "" {
		t.Fatal("X-Request-Id header missing")
	}
	if svc.lastAuthScope.TenantID != "tenant-auth" || svc.lastAuthScope.UserID != "user-auth" {
		t.Fatalf("auth scope = %+v, want authoritative scope", svc.lastAuthScope)
	}
}

func TestRecallHandler_SetsConsistencyHeader(t *testing.T) {
	handler := NewHandler(&captureService{})
	req := httptest.NewRequest(http.MethodPost, "/memory/recall/unified", bytes.NewBufferString(`{"scope":{"tenant_id":"tenant-a","user_id":"user-1"},"query":"pdf"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-Id", "tenant-a")
	req.Header.Set("X-User-Id", "user-1")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("X-Consistency-Level"); got != "eventual" {
		t.Fatalf("X-Consistency-Level = %q, want eventual", got)
	}
}

func TestRecallHandler_PreservesStrongConsistencyHeader(t *testing.T) {
	handler := NewHandler(&captureService{})
	req := httptest.NewRequest(http.MethodPost, "/memory/recall/unified", bytes.NewBufferString(`{"scope":{"tenant_id":"tenant-a","user_id":"user-1"},"query":"pdf","consistency_level":"strong"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-Id", "tenant-a")
	req.Header.Set("X-User-Id", "user-1")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("X-Consistency-Level"); got != "strong" {
		t.Fatalf("X-Consistency-Level = %q, want strong", got)
	}
}

func TestDeleteHandler_SetsConsistencyHeader(t *testing.T) {
	handler := NewHandler(&captureService{})
	req := httptest.NewRequest(http.MethodDelete, "/memory/items/mem_123", bytes.NewBufferString(`{"scope":{"tenant_id":"tenant-a","user_id":"user-1"},"expected_version":4,"consistency_level":"strong"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-Id", "tenant-a")
	req.Header.Set("X-User-Id", "user-1")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("X-Consistency-Level"); got != "strong" {
		t.Fatalf("X-Consistency-Level = %q, want strong", got)
	}
}

func TestUnpinHandler_SetsVersionHeader(t *testing.T) {
	svc := &captureService{}
	handler := NewHandler(svc)
	req := httptest.NewRequest(http.MethodPost, "/memory/items/mem_123/unpin", bytes.NewBufferString(`{"scope":{"tenant_id":"tenant-a","user_id":"user-1"},"expected_version":4}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-Id", "tenant-a")
	req.Header.Set("X-User-Id", "user-1")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("X-Memory-Version"); got != "5" {
		t.Fatalf("X-Memory-Version = %q, want 5", got)
	}
}

func TestEnableHandler_SetsVersionHeader(t *testing.T) {
	svc := &captureService{}
	handler := NewHandler(svc)
	req := httptest.NewRequest(http.MethodPost, "/memory/items/mem_123/enable", bytes.NewBufferString(`{"scope":{"tenant_id":"tenant-a","user_id":"user-1"},"expected_version":5}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-Id", "tenant-a")
	req.Header.Set("X-User-Id", "user-1")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("X-Memory-Version"); got != "6" {
		t.Fatalf("X-Memory-Version = %q, want 6", got)
	}
}

func TestHandlers_UnauthorizedWithoutScopeHeaders(t *testing.T) {
	handler := NewHandler(&captureService{})
	req := httptest.NewRequest(http.MethodPost, "/memory/recall/unified", bytes.NewBufferString(`{"scope":{"tenant_id":"tenant-a","user_id":"user-1"},"query":"pdf"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}

	var response httpapi.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response.Error.Code != "unauthorized" {
		t.Fatalf("code = %q, want unauthorized", response.Error.Code)
	}
}

func TestGetHandler_ReturnsItemAndVersionHeader(t *testing.T) {
	handler := NewHandler(&captureService{})
	req := httptest.NewRequest(http.MethodGet, "/memory/items/mem_abc", nil)
	req.Header.Set("X-Tenant-Id", "tenant-a")
	req.Header.Set("X-User-Id", "user-1")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("X-Memory-Version"); got != "7" {
		t.Fatalf("X-Memory-Version = %q, want 7", got)
	}

	var response httpapi.GetMemoryItemResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response.MemoryID != "mem_abc" {
		t.Fatalf("MemoryID = %q, want mem_abc", response.MemoryID)
	}
	if response.Kind != "semantic" {
		t.Fatalf("Kind = %q, want semantic", response.Kind)
	}
}

func TestGetHandler_UnauthorizedWithoutScopeHeaders(t *testing.T) {
	handler := NewHandler(&captureService{})
	req := httptest.NewRequest(http.MethodGet, "/memory/items/mem_abc", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}
