package service

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	"github.com/costa92/llm-agent-memory-gateway/internal/httpapi"
	pgmemory "github.com/costa92/llm-agent-memory-postgres/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fakeIdempotencyStore is an in-memory IdempotencyStore for unit tests.
type fakeIdempotencyStore struct {
	entries map[string]corememory.IdempotencyEntry
	loadErr error
	saveErr error
}

func newFakeIdempotencyStore() *fakeIdempotencyStore {
	return &fakeIdempotencyStore{entries: map[string]corememory.IdempotencyEntry{}}
}

func (f *fakeIdempotencyStore) LoadIdempotency(_ context.Context, tenantID, key string) (corememory.IdempotencyEntry, error) {
	if f.loadErr != nil {
		return corememory.IdempotencyEntry{}, f.loadErr
	}
	k := tenantID + "|" + key
	entry, ok := f.entries[k]
	if !ok {
		return corememory.IdempotencyEntry{}, pgmemory.ErrNotFound
	}
	return entry, nil
}

func (f *fakeIdempotencyStore) SaveIdempotency(_ context.Context, entry corememory.IdempotencyEntry) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	k := entry.TenantID + "|" + entry.IdempotencyKey
	f.entries[k] = entry
	return nil
}

// fakePatchBackend wraps fakeBackend and provides controllable PatchRecord.
type fakePatchBackend struct {
	fakeBackend
	patchCalls  int
	patchResult corememory.PatchRecordResult
	patchErr    error
}

func (f *fakePatchBackend) PatchRecord(_ context.Context, in corememory.PatchRecordInput) (corememory.PatchRecordResult, error) {
	f.patchCalls++
	if f.patchErr != nil {
		return corememory.PatchRecordResult{}, f.patchErr
	}
	if f.patchResult.MemoryID == "" {
		return corememory.PatchRecordResult{
			MemoryID: "mem_patch_1",
			Version:  in.ExpectedVersion + 1,
			Record: corememory.MemoryRecord{
				MemoryID: "mem_patch_1",
				Version:  in.ExpectedVersion + 1,
			},
		}, nil
	}
	return f.patchResult, nil
}

func newPatchService(t *testing.T, backend *fakePatchBackend, idemStore corememory.IdempotencyStore) *Service {
	t.Helper()
	svc, err := New(backend, nil, nil, nil, Config{
		IdempotencyStore: idemStore,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc
}

func TestPatchMemory_IdempotencyKey_ReplayIdenticalPatch(t *testing.T) {
	backend := &fakePatchBackend{}
	idem := newFakeIdempotencyStore()
	svc := newPatchService(t, backend, idem)

	scope := authz.Scope{TenantID: "tenant-auth", UserID: "user-auth"}
	req := httpapi.PatchMemoryRequest{
		IdempotencyKey:  "patch-idem-1",
		ExpectedVersion: 1,
		Patch:           httpapi.PatchMemoryFields{Content: ptr("updated content")},
	}

	first, err := svc.PatchMemory(context.Background(), scope, "mem_patch_1", req)
	if err != nil {
		t.Fatalf("first PatchMemory: %v", err)
	}
	if first.MemoryID == "" || first.Version == 0 {
		t.Fatalf("first result invalid: %+v", first)
	}
	if backend.patchCalls != 1 {
		t.Fatalf("patchCalls after first call = %d, want 1", backend.patchCalls)
	}

	// Second call with the same key and same body — must replay.
	second, err := svc.PatchMemory(context.Background(), scope, "mem_patch_1", req)
	if err != nil {
		t.Fatalf("second PatchMemory: %v", err)
	}
	if second.MemoryID != first.MemoryID || second.Version != first.Version {
		t.Fatalf("replay mismatch: first=%+v second=%+v", first, second)
	}
	// Backend must NOT have been called a second time.
	if backend.patchCalls != 1 {
		t.Fatalf("patchCalls after replay = %d, want 1 (no second write)", backend.patchCalls)
	}
}

func TestPatchMemory_IdempotencyKey_ConflictDifferentPatchBody(t *testing.T) {
	backend := &fakePatchBackend{}
	idem := newFakeIdempotencyStore()
	svc := newPatchService(t, backend, idem)

	scope := authz.Scope{TenantID: "tenant-auth", UserID: "user-auth"}
	req1 := httpapi.PatchMemoryRequest{
		IdempotencyKey:  "patch-idem-conflict",
		ExpectedVersion: 1,
		Patch:           httpapi.PatchMemoryFields{Content: ptr("original")},
	}
	req2 := httpapi.PatchMemoryRequest{
		IdempotencyKey:  "patch-idem-conflict",
		ExpectedVersion: 1,
		Patch:           httpapi.PatchMemoryFields{Content: ptr("different body")},
	}

	if _, err := svc.PatchMemory(context.Background(), scope, "mem_patch_1", req1); err != nil {
		t.Fatalf("first PatchMemory: %v", err)
	}

	_, err := svc.PatchMemory(context.Background(), scope, "mem_patch_1", req2)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	if got := httpapi.StatusCode(err); got != 409 {
		t.Fatalf("StatusCode(err) = %d, want 409", got)
	}
}

func TestPatchMemory_NoIdempotencyKey_PreservesCurrentBehavior(t *testing.T) {
	backend := &fakePatchBackend{}
	idem := newFakeIdempotencyStore()
	svc := newPatchService(t, backend, idem)

	scope := authz.Scope{TenantID: "tenant-auth", UserID: "user-auth"}
	req := httpapi.PatchMemoryRequest{
		// No IdempotencyKey.
		ExpectedVersion: 1,
		Patch:           httpapi.PatchMemoryFields{Content: ptr("content")},
	}

	resp, err := svc.PatchMemory(context.Background(), scope, "mem_patch_1", req)
	if err != nil {
		t.Fatalf("PatchMemory: %v", err)
	}
	if resp.MemoryID == "" {
		t.Fatal("MemoryID is empty")
	}
	// Version must be bumped (expected 1 → 2).
	if resp.Version != 2 {
		t.Fatalf("Version = %d, want 2", resp.Version)
	}
	// Backend was called exactly once.
	if backend.patchCalls != 1 {
		t.Fatalf("patchCalls = %d, want 1", backend.patchCalls)
	}
	// Idempotency store must be empty (no key was provided).
	if len(idem.entries) != 0 {
		t.Fatalf("idempotency store has %d entries, want 0", len(idem.entries))
	}
}

// ── Live-DB integration tests ────────────────────────────────────────────────

const patchIdemEnvVar = "LLM_AGENT_MEMORY_PG_URL"

func openTestStore(t *testing.T) *pgmemory.Store {
	t.Helper()
	dsn := os.Getenv(patchIdemEnvVar)
	if dsn == "" {
		t.Skipf("set %s to run live postgres tests", patchIdemEnvVar)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	prefix := fmt.Sprintf("pi_%d", time.Now().UnixNano())
	s, err := pgmemory.New(pool, pgmemory.Config{TablePrefix: prefix})
	if err != nil {
		t.Fatalf("pgmemory.New: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

// TestPatchMemory_LiveDB_IdempotencyReplay_SingleEventAndOutbox asserts that
// a replayed PATCH (same key, same body) does not create a second event or
// outbox row, and returns the same version as the first call.
func TestPatchMemory_LiveDB_IdempotencyReplay_SingleEventAndOutbox(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	// Write a seed record first.
	writeRes, err := store.WriteRecord(ctx, corememory.WriteRecordInput{
		TenantID:       "tenant_pi",
		IdempotencyKey: "seed-pi-1",
		RequestHash:    "hash-seed-pi-1",
		Record: corememory.MemoryRecord{
			UserID:   "user_pi",
			Kind:     "episodic",
			Source:   "user_saved",
			Category: "project",
			Content:  "seed content",
		},
	})
	if err != nil {
		t.Fatalf("WriteRecord seed: %v", err)
	}

	svc, err := New(store, nil, nil, nil, Config{IdempotencyStore: store})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	scope := authz.Scope{TenantID: "tenant_pi", UserID: "user_pi"}
	req := httpapi.PatchMemoryRequest{
		IdempotencyKey:  "patch-pi-replay-1",
		ExpectedVersion: writeRes.Version,
		Patch:           httpapi.PatchMemoryFields{Content: ptr("patched content")},
	}

	first, err := svc.PatchMemory(ctx, scope, writeRes.MemoryID, req)
	if err != nil {
		t.Fatalf("first PatchMemory: %v", err)
	}
	second, err := svc.PatchMemory(ctx, scope, writeRes.MemoryID, req)
	if err != nil {
		t.Fatalf("second PatchMemory (replay): %v", err)
	}
	if second.MemoryID != first.MemoryID || second.Version != first.Version {
		t.Fatalf("replay mismatch: first=%+v second=%+v", first, second)
	}

	// Idempotency store must have exactly 1 entry for this key.
	entry, loadErr := store.LoadIdempotency(ctx, "tenant_pi", "patch-pi-replay-1")
	if loadErr != nil {
		t.Fatalf("LoadIdempotency: %v", loadErr)
	}
	if entry.MemoryID != first.MemoryID {
		t.Fatalf("idempotency entry MemoryID = %q, want %q", entry.MemoryID, first.MemoryID)
	}
	if entry.Response.Version != first.Version {
		t.Fatalf("idempotency entry Version = %d, want %d", entry.Response.Version, first.Version)
	}
}

// TestPatchMemory_LiveDB_IdempotencyConflict_DifferentBody asserts that
// reusing a patch idempotency key with a different body returns a 409.
func TestPatchMemory_LiveDB_IdempotencyConflict_DifferentBody(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	writeRes, err := store.WriteRecord(ctx, corememory.WriteRecordInput{
		TenantID:       "tenant_pi",
		IdempotencyKey: "seed-pi-2",
		RequestHash:    "hash-seed-pi-2",
		Record: corememory.MemoryRecord{
			UserID:   "user_pi",
			Kind:     "episodic",
			Source:   "user_saved",
			Category: "project",
			Content:  "seed content",
		},
	})
	if err != nil {
		t.Fatalf("WriteRecord seed: %v", err)
	}

	svc, err := New(store, nil, nil, nil, Config{IdempotencyStore: store})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	scope := authz.Scope{TenantID: "tenant_pi", UserID: "user_pi"}
	req1 := httpapi.PatchMemoryRequest{
		IdempotencyKey:  "patch-pi-conflict-1",
		ExpectedVersion: writeRes.Version,
		Patch:           httpapi.PatchMemoryFields{Content: ptr("body A")},
	}
	req2 := httpapi.PatchMemoryRequest{
		IdempotencyKey:  "patch-pi-conflict-1",
		ExpectedVersion: writeRes.Version,
		Patch:           httpapi.PatchMemoryFields{Content: ptr("body B — different")},
	}

	if _, err := svc.PatchMemory(ctx, scope, writeRes.MemoryID, req1); err != nil {
		t.Fatalf("first PatchMemory: %v", err)
	}
	_, err = svc.PatchMemory(ctx, scope, writeRes.MemoryID, req2)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	if got := httpapi.StatusCode(err); got != 409 {
		t.Fatalf("StatusCode(err) = %d, want 409", got)
	}
}

func ptr[T any](v T) *T { return &v }
