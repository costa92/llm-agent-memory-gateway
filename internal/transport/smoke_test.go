package transport

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	"github.com/costa92/llm-agent-memory-gateway/internal/httpapi"
	"github.com/costa92/llm-agent-memory-gateway/internal/observability"
	"github.com/costa92/llm-agent-memory-gateway/internal/service"
	pgmemory "github.com/costa92/llm-agent-memory-postgres/postgres"
)

type inMemoryGateway struct {
	version int64
	deleted bool
}

func (g *inMemoryGateway) RecallUnified(context.Context, authz.Scope, httpapi.RecallUnifiedRequest) (httpapi.RecallUnifiedResponse, error) {
	if g.deleted || g.version == 0 {
		return httpapi.RecallUnifiedResponse{Hits: []httpapi.RecallHitResponse{}}, nil
	}
	return httpapi.RecallUnifiedResponse{
		Hits: []httpapi.RecallHitResponse{
			{
				MemoryID: "mem_123",
				Kind:     "semantic",
				Version:  g.version,
				Content:  "remembered",
				Metadata: httpapi.RecallHitMetadata{TokenCostEstimate: 3},
			},
		},
		Trace: &httpapi.RecallTraceResponse{ConsistencyLevel: "eventual"},
	}, nil
}

func (g *inMemoryGateway) WriteMemory(context.Context, authz.Scope, httpapi.WriteMemoryRequest) (httpapi.WriteMemoryResponse, error) {
	g.version = 1
	g.deleted = false
	return httpapi.WriteMemoryResponse{Memory: httpapi.WriteMemoryResult{MemoryID: "mem_123", Version: g.version, Status: "saved"}}, nil
}
func (g *inMemoryGateway) PatchMemory(context.Context, authz.Scope, string, httpapi.PatchMemoryRequest) (httpapi.PatchMemoryResponse, error) {
	g.version++
	return httpapi.PatchMemoryResponse{MemoryID: "mem_123", Version: g.version}, nil
}
func (g *inMemoryGateway) PinMemory(context.Context, authz.Scope, string, httpapi.PinMemoryRequest) (httpapi.PinMemoryResponse, error) {
	g.version++
	return httpapi.PinMemoryResponse{MemoryID: "mem_123", Version: g.version, Pinned: true}, nil
}
func (g *inMemoryGateway) UnpinMemory(context.Context, authz.Scope, string, httpapi.PinMemoryRequest) (httpapi.PinMemoryResponse, error) {
	g.version++
	return httpapi.PinMemoryResponse{MemoryID: "mem_123", Version: g.version, Pinned: false}, nil
}
func (g *inMemoryGateway) DisableMemory(context.Context, authz.Scope, string, httpapi.DisableMemoryRequest) (httpapi.DisableMemoryResponse, error) {
	g.version++
	return httpapi.DisableMemoryResponse{MemoryID: "mem_123", Version: g.version, Disabled: true}, nil
}
func (g *inMemoryGateway) EnableMemory(context.Context, authz.Scope, string, httpapi.DisableMemoryRequest) (httpapi.DisableMemoryResponse, error) {
	g.version++
	return httpapi.DisableMemoryResponse{MemoryID: "mem_123", Version: g.version, Disabled: false}, nil
}
func (g *inMemoryGateway) DeleteMemory(context.Context, authz.Scope, string, httpapi.DeleteMemoryRequest) (httpapi.DeleteMemoryResponse, error) {
	g.version++
	g.deleted = true
	return httpapi.DeleteMemoryResponse{MemoryID: "mem_123", Deleted: true, Version: g.version}, nil
}
func (g *inMemoryGateway) CloseSession(context.Context, authz.Scope, string, httpapi.SessionCloseRequest) (httpapi.SessionCloseResponse, error) {
	return httpapi.SessionCloseResponse{SessionID: "sess_9", Status: "closed"}, nil
}
func (g *inMemoryGateway) HeartbeatSession(context.Context, authz.Scope, string, httpapi.SessionHeartbeatRequest) (httpapi.SessionHeartbeatResponse, error) {
	return httpapi.SessionHeartbeatResponse{SessionID: "sess_10", Status: "active"}, nil
}
func (g *inMemoryGateway) GetMemoryItem(_ context.Context, _ authz.Scope, memoryID string) (httpapi.GetMemoryItemResponse, error) {
	if g.deleted || g.version == 0 {
		return httpapi.GetMemoryItemResponse{}, httpapi.ErrNotFound("memory record not found", nil)
	}
	return httpapi.GetMemoryItemResponse{MemoryID: memoryID, Kind: "semantic", Version: g.version, Content: "remembered"}, nil
}

func TestSmoke_WriteRecallPinDisableDeleteClose(t *testing.T) {
	handler := NewHandler(&inMemoryGateway{})

	cases := []struct {
		method string
		path   string
		body   string
		want   int
	}{
		{method: http.MethodPost, path: "/memory/write", body: `{"idempotency_key":"idem_1","scope":{"tenant_id":"tenant-a","user_id":"user-1"},"record":{"kind":"semantic","source":"user_saved","category":"project","content":"remember"}}`, want: http.StatusOK},
		{method: http.MethodGet, path: "/memory/items/mem_123", body: ``, want: http.StatusOK},
		{method: http.MethodPost, path: "/memory/recall/unified", body: `{"scope":{"tenant_id":"tenant-a","user_id":"user-1"},"query":"remember"}`, want: http.StatusOK},
		{method: http.MethodPost, path: "/memory/items/mem_123/pin", body: `{"scope":{"tenant_id":"tenant-a","user_id":"user-1"},"expected_version":1}`, want: http.StatusOK},
		{method: http.MethodPost, path: "/memory/items/mem_123/unpin", body: `{"scope":{"tenant_id":"tenant-a","user_id":"user-1"},"expected_version":2}`, want: http.StatusOK},
		{method: http.MethodPost, path: "/memory/items/mem_123/disable", body: `{"scope":{"tenant_id":"tenant-a","user_id":"user-1"},"expected_version":3}`, want: http.StatusOK},
		{method: http.MethodPost, path: "/memory/items/mem_123/enable", body: `{"scope":{"tenant_id":"tenant-a","user_id":"user-1"},"expected_version":4}`, want: http.StatusOK},
		{method: http.MethodDelete, path: "/memory/items/mem_123", body: `{"scope":{"tenant_id":"tenant-a","user_id":"user-1"},"expected_version":5,"consistency_level":"strong"}`, want: http.StatusOK},
		{method: http.MethodPost, path: "/memory/sessions/sess_9/close", body: `{"scope":{"tenant_id":"tenant-a","user_id":"user-1","session_id":"sess_9"},"mode":"expire_working"}`, want: http.StatusOK},
		{method: http.MethodPost, path: "/memory/sessions/sess_10/heartbeat", body: `{"scope":{"tenant_id":"tenant-a","user_id":"user-1","session_id":"sess_10"}}`, want: http.StatusOK},
	}

	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Tenant-Id", "tenant-a")
		req.Header.Set("X-User-Id", "user-1")
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, req)

		if recorder.Code != tc.want {
			t.Fatalf("%s %s status = %d, want %d", tc.method, tc.path, recorder.Code, tc.want)
		}
	}
}

func TestSmoke_CloseThenRecallIsRejected(t *testing.T) {
	svc, err := service.New(&fakeBackendForTransport{}, &fakeRecallerForTransport{}, &fakeSessionCloserForTransport{}, nil, service.Config{})
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	handler := NewHandler(svc)

	closeReq := httptest.NewRequest(http.MethodPost, "/memory/sessions/sess_9/close", bytes.NewBufferString(`{"scope":{"tenant_id":"tenant-a","user_id":"user-1","session_id":"sess_9"},"mode":"expire_working"}`))
	closeReq.Header.Set("Content-Type", "application/json")
	closeReq.Header.Set("X-Tenant-Id", "tenant-a")
	closeReq.Header.Set("X-User-Id", "user-1")
	closeReq.Header.Set("X-Session-Id", "sess_9")
	closeRecorder := httptest.NewRecorder()
	handler.ServeHTTP(closeRecorder, closeReq)
	if closeRecorder.Code != http.StatusOK {
		t.Fatalf("close status = %d, want %d", closeRecorder.Code, http.StatusOK)
	}

	recallReq := httptest.NewRequest(http.MethodPost, "/memory/recall/unified", bytes.NewBufferString(`{"scope":{"tenant_id":"tenant-a","user_id":"user-1","session_id":"sess_9"},"query":"pdf"}`))
	recallReq.Header.Set("Content-Type", "application/json")
	recallReq.Header.Set("X-Tenant-Id", "tenant-a")
	recallReq.Header.Set("X-User-Id", "user-1")
	recallReq.Header.Set("X-Session-Id", "sess_9")
	recallRecorder := httptest.NewRecorder()
	handler.ServeHTTP(recallRecorder, recallReq)
	if recallRecorder.Code != http.StatusForbidden {
		t.Fatalf("recall status = %d, want %d", recallRecorder.Code, http.StatusForbidden)
	}
}

func TestSmoke_MetricsReflectRecallCacheLifecycle(t *testing.T) {
	backend := newStatefulBackendForTransport()
	recaller := &statefulRecallerForTransport{backend: backend}
	metrics := observability.NewMetrics()
	svc, err := service.New(backend, recaller, nil, observability.ComposeTraceEmitters(metrics.TraceEmitter()), service.Config{
		RecallObserver:      metrics.RecallObserver(),
		RecallCacheObserver: metrics.RecallCacheObserver(),
	})
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	handler := NewHandler(svc, func(mux *http.ServeMux) {
		mux.Handle("GET /metrics", metrics.Handler())
	})

	requests := []struct {
		method string
		path   string
		body   string
		want   int
	}{
		{method: http.MethodPost, path: "/memory/write", body: `{"idempotency_key":"idem_1","scope":{"tenant_id":"tenant-a","user_id":"user-1"},"record":{"kind":"semantic","source":"user_saved","category":"project","content":"remember first"}}`, want: http.StatusOK},
		{method: http.MethodPost, path: "/memory/recall/unified", body: `{"scope":{"tenant_id":"tenant-a","user_id":"user-1"},"query":"remember","consistency_level":"eventual"}`, want: http.StatusOK},
		{method: http.MethodPost, path: "/memory/recall/unified", body: `{"scope":{"tenant_id":"tenant-a","user_id":"user-1"},"query":"remember","consistency_level":"eventual"}`, want: http.StatusOK},
		{method: http.MethodPost, path: "/memory/write", body: `{"idempotency_key":"idem_2","scope":{"tenant_id":"tenant-a","user_id":"user-1"},"record":{"kind":"semantic","source":"user_saved","category":"project","content":"remember second"}}`, want: http.StatusOK},
		{method: http.MethodPost, path: "/memory/recall/unified", body: `{"scope":{"tenant_id":"tenant-a","user_id":"user-1"},"query":"remember","consistency_level":"eventual"}`, want: http.StatusOK},
	}

	for _, tc := range requests {
		req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Tenant-Id", "tenant-a")
		req.Header.Set("X-User-Id", "user-1")
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, req)

		if recorder.Code != tc.want {
			t.Fatalf("%s %s status = %d, want %d", tc.method, tc.path, recorder.Code, tc.want)
		}
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRecorder := httptest.NewRecorder()
	handler.ServeHTTP(metricsRecorder, metricsReq)
	if metricsRecorder.Code != http.StatusOK {
		t.Fatalf("GET /metrics status = %d, want %d", metricsRecorder.Code, http.StatusOK)
	}

	body := metricsRecorder.Body.String()
	for _, want := range []string{
		"recall_origin_total 2",
		"recall_l1_hit_total 1",
		"recall_cache_fill_total 2",
		"recall_invalidation_total 2",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics output missing %q\n%s", want, body)
		}
	}
	if recaller.calls != 2 {
		t.Fatalf("recaller calls = %d, want 2", recaller.calls)
	}
}

type fakeBackendForTransport struct{}

func (fakeBackendForTransport) GetRecord(context.Context, string, string) (corememory.MemoryRecord, error) {
	return corememory.MemoryRecord{}, nil
}
func (fakeBackendForTransport) GetRecordIncludingHidden(context.Context, string, string) (corememory.MemoryRecord, error) {
	return corememory.MemoryRecord{}, nil
}
func (fakeBackendForTransport) WriteRecord(context.Context, corememory.WriteRecordInput) (corememory.WriteRecordResult, error) {
	return corememory.WriteRecordResult{}, nil
}
func (fakeBackendForTransport) PatchRecord(context.Context, corememory.PatchRecordInput) (corememory.PatchRecordResult, error) {
	return corememory.PatchRecordResult{}, nil
}
func (fakeBackendForTransport) DeleteRecord(context.Context, corememory.DeleteRecordInput) (corememory.DeleteRecordResult, error) {
	return corememory.DeleteRecordResult{}, nil
}
func (fakeBackendForTransport) PinRecord(context.Context, corememory.PinRecordInput) (corememory.PinRecordResult, error) {
	return corememory.PinRecordResult{}, nil
}
func (fakeBackendForTransport) DisableRecord(context.Context, corememory.DisableRecordInput) (corememory.DisableRecordResult, error) {
	return corememory.DisableRecordResult{}, nil
}

type fakeRecallerForTransport struct{}

func (fakeRecallerForTransport) Recall(context.Context, authz.Scope, string, int) ([]corememory.MemoryRecord, error) {
	return []corememory.MemoryRecord{{MemoryID: "mem_1", Content: "remembered", Kind: "semantic", Source: "user_saved", Category: "project", Version: 1}}, nil
}

type fakeSessionCloserForTransport struct{}

func (fakeSessionCloserForTransport) CloseSession(context.Context, authz.Scope, string) error {
	return nil
}

type statefulBackendForTransport struct {
	mu      sync.Mutex
	nextID  int64
	records map[string]corememory.MemoryRecord
}

func newStatefulBackendForTransport() *statefulBackendForTransport {
	return &statefulBackendForTransport{
		records: map[string]corememory.MemoryRecord{},
	}
}

func (b *statefulBackendForTransport) GetRecord(_ context.Context, tenantID, memoryID string) (corememory.MemoryRecord, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	record, ok := b.records[memoryID]
	if !ok || record.TenantID != tenantID || record.Deleted {
		return corememory.MemoryRecord{}, pgmemory.ErrNotFound
	}
	return record, nil
}

func (b *statefulBackendForTransport) GetRecordIncludingHidden(_ context.Context, tenantID, memoryID string) (corememory.MemoryRecord, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	record, ok := b.records[memoryID]
	if !ok || record.TenantID != tenantID {
		return corememory.MemoryRecord{}, pgmemory.ErrNotFound
	}
	return record, nil
}

func (b *statefulBackendForTransport) WriteRecord(_ context.Context, in corememory.WriteRecordInput) (corememory.WriteRecordResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.nextID++
	record := in.Record
	record.MemoryID = fmt.Sprintf("mem_%d", b.nextID)
	record.Version = 1
	record.TenantID = in.TenantID
	b.records[record.MemoryID] = record
	return corememory.WriteRecordResult{
		MemoryID: record.MemoryID,
		Version:  record.Version,
		Created:  true,
		Record:   record,
	}, nil
}

func (b *statefulBackendForTransport) PatchRecord(context.Context, corememory.PatchRecordInput) (corememory.PatchRecordResult, error) {
	return corememory.PatchRecordResult{}, nil
}

func (b *statefulBackendForTransport) DeleteRecord(context.Context, corememory.DeleteRecordInput) (corememory.DeleteRecordResult, error) {
	return corememory.DeleteRecordResult{}, nil
}

func (b *statefulBackendForTransport) PinRecord(context.Context, corememory.PinRecordInput) (corememory.PinRecordResult, error) {
	return corememory.PinRecordResult{}, nil
}

func (b *statefulBackendForTransport) DisableRecord(context.Context, corememory.DisableRecordInput) (corememory.DisableRecordResult, error) {
	return corememory.DisableRecordResult{}, nil
}

func (b *statefulBackendForTransport) list(scope authz.Scope) []corememory.MemoryRecord {
	b.mu.Lock()
	defer b.mu.Unlock()

	records := make([]corememory.MemoryRecord, 0, len(b.records))
	for _, record := range b.records {
		if record.TenantID != scope.TenantID || record.UserID != scope.UserID || record.Deleted || record.Disabled {
			continue
		}
		records = append(records, record)
	}
	return records
}

type statefulRecallerForTransport struct {
	backend *statefulBackendForTransport
	calls   int
}

func (r *statefulRecallerForTransport) Recall(_ context.Context, scope authz.Scope, _ string, topK int) ([]corememory.MemoryRecord, error) {
	r.calls++
	records := r.backend.list(scope)
	if topK > 0 && len(records) > topK {
		records = records[:topK]
	}
	return records, nil
}
