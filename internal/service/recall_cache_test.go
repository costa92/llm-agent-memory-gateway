package service

import (
	"context"
	"testing"
	"time"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	"github.com/costa92/llm-agent-memory-gateway/internal/httpapi"
)

type fakeRecallObserver struct {
	events []RecallObservation
}

func (f *fakeRecallObserver) ObserveRecall(_ context.Context, obs RecallObservation) {
	f.events = append(f.events, obs)
}

type fakeRecallCacheObserver struct {
	events []RecallCacheObservation
}

func (f *fakeRecallCacheObserver) ObserveRecallCache(_ context.Context, obs RecallCacheObservation) {
	f.events = append(f.events, obs)
}

func TestRecallUnified_EventualCanHitCache(t *testing.T) {
	recaller := &fakeRecaller{
		records: []corememory.MemoryRecord{
			{MemoryID: "mem_1", Content: "cached note", Kind: "semantic", Source: "user_saved", Category: "project", Version: 1},
		},
	}
	observer := &fakeRecallObserver{}
	svc, err := New(&fakeBackend{}, recaller, nil, nil, Config{RecallObserver: observer})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	scope := authz.Scope{TenantID: "tenant-a", UserID: "user-1"}
	req := httpapi.RecallUnifiedRequest{Query: "cached", ConsistencyLevel: "eventual"}

	first, err := svc.RecallUnified(context.Background(), scope, req)
	if err != nil {
		t.Fatalf("first RecallUnified() error = %v", err)
	}
	second, err := svc.RecallUnified(context.Background(), scope, req)
	if err != nil {
		t.Fatalf("second RecallUnified() error = %v", err)
	}

	if len(first.Hits) != 1 || len(second.Hits) != 1 {
		t.Fatalf("hits = %d/%d, want 1/1", len(first.Hits), len(second.Hits))
	}
	if recaller.calls != 1 {
		t.Fatalf("recaller.calls = %d, want 1", recaller.calls)
	}
	if len(observer.events) != 2 || observer.events[1].CacheLevel != "l1_hit" {
		t.Fatalf("observer events = %+v", observer.events)
	}
}

func TestRecallUnified_StrongBypassesCache(t *testing.T) {
	recaller := &fakeRecaller{
		records: []corememory.MemoryRecord{
			{MemoryID: "mem_1", Content: "strong note", Kind: "semantic", Source: "user_saved", Category: "project", Version: 1},
		},
	}
	svc, err := New(&fakeBackend{}, recaller, nil, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	scope := authz.Scope{TenantID: "tenant-a", UserID: "user-1"}
	req := httpapi.RecallUnifiedRequest{Query: "strong", ConsistencyLevel: "strong"}

	if _, err := svc.RecallUnified(context.Background(), scope, req); err != nil {
		t.Fatalf("first RecallUnified() error = %v", err)
	}
	if _, err := svc.RecallUnified(context.Background(), scope, req); err != nil {
		t.Fatalf("second RecallUnified() error = %v", err)
	}

	if recaller.calls != 2 {
		t.Fatalf("recaller.calls = %d, want 2", recaller.calls)
	}
}

func TestWriteMemory_InvalidatesRecallCache(t *testing.T) {
	recaller := &fakeRecaller{
		records: []corememory.MemoryRecord{
			{MemoryID: "mem_1", Content: "before write", Kind: "semantic", Source: "user_saved", Category: "project", Version: 1},
		},
	}
	backend := &fakeBackend{}
	cacheObserver := &fakeRecallCacheObserver{}
	svc, err := New(backend, recaller, nil, nil, Config{RecallCacheObserver: cacheObserver})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	scope := authz.Scope{TenantID: "tenant-a", UserID: "user-1"}
	recallReq := httpapi.RecallUnifiedRequest{Query: "before", ConsistencyLevel: "eventual"}

	if _, err := svc.RecallUnified(context.Background(), scope, recallReq); err != nil {
		t.Fatalf("RecallUnified() error = %v", err)
	}

	recaller.records = []corememory.MemoryRecord{
		{MemoryID: "mem_2", Content: "after write", Kind: "semantic", Source: "user_saved", Category: "project", Version: 2},
	}
	if _, err := svc.WriteMemory(context.Background(), scope, httpapi.WriteMemoryRequest{
		IdempotencyKey: "idem_1",
		Record: httpapi.WriteRecordPayload{
			Kind:     "semantic",
			Source:   "user_saved",
			Category: "project",
			Content:  "new write",
		},
	}); err != nil {
		t.Fatalf("WriteMemory() error = %v", err)
	}
	resp, err := svc.RecallUnified(context.Background(), scope, recallReq)
	if err != nil {
		t.Fatalf("RecallUnified() after write error = %v", err)
	}

	if recaller.calls != 2 {
		t.Fatalf("recaller.calls = %d, want 2", recaller.calls)
	}
	if len(resp.Hits) != 1 || resp.Hits[0].MemoryID != "mem_2" {
		t.Fatalf("hits = %+v, want mem_2", resp.Hits)
	}
	foundInvalidate := false
	for _, event := range cacheObserver.events {
		if event.Action == "invalidate" {
			foundInvalidate = true
			break
		}
	}
	if !foundInvalidate {
		t.Fatalf("cache observer events = %+v", cacheObserver.events)
	}
}

func TestRecallUnified_FillsRecallCacheObserverOnOriginRead(t *testing.T) {
	recaller := &fakeRecaller{
		records: []corememory.MemoryRecord{
			{MemoryID: "mem_1", Content: "origin note", Kind: "semantic", Source: "user_saved", Category: "project", Version: 1},
		},
	}
	cacheObserver := &fakeRecallCacheObserver{}
	svc, err := New(&fakeBackend{}, recaller, nil, nil, Config{RecallCacheObserver: cacheObserver})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	scope := authz.Scope{TenantID: "tenant-a", UserID: "user-1"}
	req := httpapi.RecallUnifiedRequest{Query: "origin", ConsistencyLevel: "eventual"}
	if _, err := svc.RecallUnified(context.Background(), scope, req); err != nil {
		t.Fatalf("RecallUnified() error = %v", err)
	}

	if len(cacheObserver.events) == 0 || cacheObserver.events[len(cacheObserver.events)-1].Action != "fill" {
		t.Fatalf("cache observer events = %+v", cacheObserver.events)
	}
}

func TestRecallUnified_BoundedRejectsExpiredCache(t *testing.T) {
	recaller := &fakeRecaller{
		records: []corememory.MemoryRecord{
			{MemoryID: "mem_fresh", Content: "fresh note", Kind: "semantic", Source: "user_saved", Category: "project", Version: 2},
		},
	}
	svc, err := New(&fakeBackend{}, recaller, nil, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	scope := authz.Scope{TenantID: "tenant-a", UserID: "user-1"}
	req := httpapi.RecallUnifiedRequest{Query: "fresh", ConsistencyLevel: "bounded"}
	cacheKey := buildRecallCacheKey(scope, req)
	svc.recallCache.Set(cacheKey, httpapi.RecallUnifiedResponse{
		Hits: []httpapi.RecallHitResponse{{MemoryID: "mem_stale"}},
		Trace: &httpapi.RecallTraceResponse{
			CacheLevel:       "l1_hit",
			ConsistencyLevel: "bounded",
		},
	}, time.Now().Add(-2*time.Minute), 0)

	resp, err := svc.RecallUnified(context.Background(), scope, req)
	if err != nil {
		t.Fatalf("RecallUnified() error = %v", err)
	}
	if recaller.calls != 1 {
		t.Fatalf("recaller.calls = %d, want 1", recaller.calls)
	}
	if len(resp.Hits) != 1 || resp.Hits[0].MemoryID != "mem_fresh" {
		t.Fatalf("hits = %+v, want mem_fresh", resp.Hits)
	}
}

func TestRecallUnified_EventualCanServeExpiredCacheWhenAllowed(t *testing.T) {
	recaller := &fakeRecaller{
		records: []corememory.MemoryRecord{
			{MemoryID: "mem_origin", Content: "origin note", Kind: "semantic", Source: "user_saved", Category: "project", Version: 2},
		},
	}
	observer := &fakeRecallObserver{}
	svc, err := New(&fakeBackend{}, recaller, nil, nil, Config{RecallObserver: observer})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	scope := authz.Scope{TenantID: "tenant-a", UserID: "user-1"}
	req := httpapi.RecallUnifiedRequest{Query: "origin", ConsistencyLevel: "eventual", AllowStaleCache: true}
	cacheKey := buildRecallCacheKey(scope, req)
	svc.recallCache.Set(cacheKey, httpapi.RecallUnifiedResponse{
		Hits: []httpapi.RecallHitResponse{{MemoryID: "mem_stale"}},
		Trace: &httpapi.RecallTraceResponse{
			CacheLevel:       "l1_hit",
			ConsistencyLevel: "eventual",
		},
	}, time.Now().Add(-2*time.Minute), 0)

	resp, err := svc.RecallUnified(context.Background(), scope, req)
	if err != nil {
		t.Fatalf("RecallUnified() error = %v", err)
	}
	if recaller.calls != 0 {
		t.Fatalf("recaller.calls = %d, want 0", recaller.calls)
	}
	if len(resp.Hits) != 1 || resp.Hits[0].MemoryID != "mem_stale" {
		t.Fatalf("hits = %+v, want mem_stale", resp.Hits)
	}
	if resp.Trace == nil || !resp.Trace.StaleServed {
		t.Fatalf("trace = %+v, want stale_served=true", resp.Trace)
	}
	if len(observer.events) != 1 || !observer.events[0].StaleServed {
		t.Fatalf("observer events = %+v", observer.events)
	}
}

func TestRecallUnified_BoundedRejectsVersionStaleCache(t *testing.T) {
	recaller := &fakeRecaller{
		records: []corememory.MemoryRecord{
			{MemoryID: "mem_fresh", Content: "fresh note", Kind: "semantic", Source: "user_saved", Category: "project", Version: 3},
		},
	}
	scopeVersionStore := newFakeScopeVersionStore()
	backend := &fakeBackend{
		records: map[string]corememory.MemoryRecord{
			"mem_stale": {MemoryID: "mem_stale", TenantID: "tenant-a", Version: 2},
		},
	}
	svc, err := New(backend, recaller, nil, nil, Config{
		ScopeVersionStore: scopeVersionStore,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	scope := authz.Scope{TenantID: "tenant-a", UserID: "user-1"}
	req := httpapi.RecallUnifiedRequest{Query: "fresh", ConsistencyLevel: "bounded"}
	cacheKey := buildRecallCacheKey(scope, req)
	svc.recallCache.Set(cacheKey, httpapi.RecallUnifiedResponse{
		Hits: []httpapi.RecallHitResponse{{MemoryID: "mem_stale"}},
		Trace: &httpapi.RecallTraceResponse{
			CacheLevel:       "l1_hit",
			ConsistencyLevel: "bounded",
		},
	}, time.Now().UTC(), 1)
	scopeVersionStore.versions[sessionScopeKey(scope)] = 2

	resp, err := svc.RecallUnified(context.Background(), scope, req)
	if err != nil {
		t.Fatalf("RecallUnified() error = %v", err)
	}
	if recaller.calls != 1 {
		t.Fatalf("recaller.calls = %d, want 1", recaller.calls)
	}
	if len(resp.Hits) != 1 || resp.Hits[0].MemoryID != "mem_fresh" {
		t.Fatalf("hits = %+v, want mem_fresh", resp.Hits)
	}
}

func TestRecallUnified_BoundedRejectsMissingRecordFromCachedHit(t *testing.T) {
	recaller := &fakeRecaller{
		records: []corememory.MemoryRecord{
			{MemoryID: "mem_origin", Content: "origin note", Kind: "semantic", Source: "user_saved", Category: "project", Version: 4},
		},
	}
	scopeVersionStore := newFakeScopeVersionStore()
	svc, err := New(&fakeBackend{}, recaller, nil, nil, Config{
		ScopeVersionStore: scopeVersionStore,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	scope := authz.Scope{TenantID: "tenant-a", UserID: "user-1"}
	req := httpapi.RecallUnifiedRequest{Query: "origin", ConsistencyLevel: "bounded"}
	cacheKey := buildRecallCacheKey(scope, req)
	svc.recallCache.Set(cacheKey, httpapi.RecallUnifiedResponse{
		Hits: []httpapi.RecallHitResponse{{MemoryID: "mem_hidden", Version: 7}},
	}, time.Now().UTC(), 0)

	resp, err := svc.RecallUnified(context.Background(), scope, req)
	if err != nil {
		t.Fatalf("RecallUnified() error = %v", err)
	}
	if recaller.calls != 1 {
		t.Fatalf("recaller.calls = %d, want 1", recaller.calls)
	}
	if len(resp.Hits) != 1 || resp.Hits[0].MemoryID != "mem_origin" {
		t.Fatalf("hits = %+v, want mem_origin", resp.Hits)
	}
}

func TestRecallUnified_BoundedCountsCacheHit(t *testing.T) {
	recaller := &fakeRecaller{
		records: []corememory.MemoryRecord{
			{MemoryID: "mem_1", Content: "bounded note", Kind: "semantic", Source: "user_saved", Category: "project", Version: 1},
		},
	}
	observer := &fakeRecallObserver{}
	scopeVersionStore := newFakeScopeVersionStore()
	backend := &fakeBackend{
		records: map[string]corememory.MemoryRecord{
			"mem_1": {MemoryID: "mem_1", TenantID: "tenant-a", Version: 1},
		},
	}
	svc, err := New(backend, recaller, nil, nil, Config{
		RecallObserver:    observer,
		ScopeVersionStore: scopeVersionStore,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	scope := authz.Scope{TenantID: "tenant-a", UserID: "user-1"}
	req := httpapi.RecallUnifiedRequest{Query: "bounded", ConsistencyLevel: "bounded"}

	if _, err := svc.RecallUnified(context.Background(), scope, req); err != nil {
		t.Fatalf("first RecallUnified() error = %v", err)
	}
	if _, err := svc.RecallUnified(context.Background(), scope, req); err != nil {
		t.Fatalf("second RecallUnified() error = %v", err)
	}

	if len(observer.events) != 2 || observer.events[1].CacheLevel != "l2_hit" {
		t.Fatalf("observer events = %+v", observer.events)
	}
}
