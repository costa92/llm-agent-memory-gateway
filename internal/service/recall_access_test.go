package service

import (
	"context"
	"testing"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	"github.com/costa92/llm-agent-memory-gateway/internal/httpapi"
)

// accessMarkingBackend embeds fakeBackend and additionally satisfies
// corememory.AccessMarker, recording the MarkAccess calls it receives. It is
// scoped to the recall-access tests so the plain fakeBackend (which does NOT
// implement AccessMarker) keeps the other recall tests' behavior unchanged.
type accessMarkingBackend struct {
	fakeBackend
	markCalls int
	markedIDs []string
}

func (b *accessMarkingBackend) MarkAccess(_ context.Context, in corememory.MarkAccessInput) error {
	b.markCalls++
	b.markedIDs = append([]string(nil), in.MemoryIDs...)
	return nil
}

// TestRecallUnified_MarksSelectedHitsAccessed asserts that an origin recall
// marks every returned hit accessed exactly once, via the backend's
// AccessMarker capability.
func TestRecallUnified_MarksSelectedHitsAccessed(t *testing.T) {
	backend := &accessMarkingBackend{}
	recaller := &fakeRecaller{
		records: []corememory.MemoryRecord{
			{MemoryID: "mem_1", Content: "a", Kind: "semantic", Source: "user_saved", Category: "project", Version: 1},
			{MemoryID: "mem_2", Content: "b", Kind: "episodic", Source: "user_saved", Category: "project", Version: 1},
		},
	}
	svc, err := New(backend, recaller, nil, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	scope := authz.Scope{TenantID: "tenant-a", UserID: "user-1"}
	resp, err := svc.RecallUnified(context.Background(), scope,
		httpapi.RecallUnifiedRequest{Query: "q", ConsistencyLevel: "strong"})
	if err != nil {
		t.Fatalf("RecallUnified() error = %v", err)
	}

	if backend.markCalls != 1 {
		t.Fatalf("markCalls = %d, want 1", backend.markCalls)
	}
	if len(backend.markedIDs) != len(resp.Hits) {
		t.Fatalf("markedIDs = %v, want one per returned hit (%d)", backend.markedIDs, len(resp.Hits))
	}
	want := map[string]bool{"mem_1": true, "mem_2": true}
	for _, id := range backend.markedIDs {
		if !want[id] {
			t.Errorf("unexpected marked id %q", id)
		}
	}
}

// TestRecallUnified_CacheHitDoesNotMarkAccessAgain asserts the cache-hit path
// returns the cached response without re-marking access (only the origin
// recall marks).
func TestRecallUnified_CacheHitDoesNotMarkAccessAgain(t *testing.T) {
	backend := &accessMarkingBackend{}
	recaller := &fakeRecaller{
		records: []corememory.MemoryRecord{
			{MemoryID: "mem_1", Content: "a", Kind: "semantic", Source: "user_saved", Category: "project", Version: 1},
		},
	}
	svc, err := New(backend, recaller, nil, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	scope := authz.Scope{TenantID: "tenant-a", UserID: "user-1"}
	req := httpapi.RecallUnifiedRequest{Query: "q", ConsistencyLevel: "eventual"}

	if _, err := svc.RecallUnified(context.Background(), scope, req); err != nil {
		t.Fatalf("first RecallUnified() error = %v", err)
	}
	if _, err := svc.RecallUnified(context.Background(), scope, req); err != nil {
		t.Fatalf("second RecallUnified() error = %v", err)
	}

	if recaller.calls != 1 {
		t.Fatalf("recaller.calls = %d, want 1 (second served from cache)", recaller.calls)
	}
	if backend.markCalls != 1 {
		t.Fatalf("markCalls = %d, want 1 (cache hit must not re-mark access)", backend.markCalls)
	}
}
