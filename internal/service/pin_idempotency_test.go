package service

import (
	"context"
	"testing"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	"github.com/costa92/llm-agent-memory-gateway/internal/httpapi"
	pgmemory "github.com/costa92/llm-agent-memory-postgres/postgres"
)

// recordingPinBackend is a stateful, recording DurableBackend used to exercise
// the terminal-state short-circuit on pin/unpin/enable. It honors optimistic
// concurrency (ExpectedVersion != current.Version → ErrVersionConflict), bumps
// version on mutation, and makes deleted/disabled records invisible to
// GetRecord (matching the real backend's visibility filter).
type recordingPinBackend struct {
	records     map[string]corememory.MemoryRecord
	pinCalls    int
	unpinCalls  int
	enableCalls int
}

func newRecordingPinBackend() *recordingPinBackend {
	return &recordingPinBackend{records: map[string]corememory.MemoryRecord{}}
}

func (f *recordingPinBackend) GetRecord(_ context.Context, _ string, memoryID string) (corememory.MemoryRecord, error) {
	rec, ok := f.records[memoryID]
	if !ok || rec.Deleted || rec.Disabled {
		return corememory.MemoryRecord{}, pgmemory.ErrNotFound
	}
	return rec, nil
}

func (f *recordingPinBackend) WriteRecord(context.Context, corememory.WriteRecordInput) (corememory.WriteRecordResult, error) {
	return corememory.WriteRecordResult{}, nil
}

func (f *recordingPinBackend) PatchRecord(context.Context, corememory.PatchRecordInput) (corememory.PatchRecordResult, error) {
	return corememory.PatchRecordResult{}, nil
}

func (f *recordingPinBackend) DeleteRecord(context.Context, corememory.DeleteRecordInput) (corememory.DeleteRecordResult, error) {
	return corememory.DeleteRecordResult{}, nil
}

func (f *recordingPinBackend) PinRecord(_ context.Context, in corememory.PinRecordInput) (corememory.PinRecordResult, error) {
	if in.Pinned {
		f.pinCalls++
	} else {
		f.unpinCalls++
	}
	rec, ok := f.records[in.MemoryID]
	if !ok {
		return corememory.PinRecordResult{}, pgmemory.ErrNotFound
	}
	if rec.Version != in.ExpectedVersion {
		return corememory.PinRecordResult{}, pgmemory.ErrVersionConflict
	}
	rec.Pinned = in.Pinned
	rec.Version++
	f.records[in.MemoryID] = rec
	return corememory.PinRecordResult{MemoryID: rec.MemoryID, Version: rec.Version, Record: rec}, nil
}

func (f *recordingPinBackend) DisableRecord(_ context.Context, in corememory.DisableRecordInput) (corememory.DisableRecordResult, error) {
	f.enableCalls++
	rec, ok := f.records[in.MemoryID]
	if !ok {
		return corememory.DisableRecordResult{}, pgmemory.ErrNotFound
	}
	if rec.Version != in.ExpectedVersion {
		return corememory.DisableRecordResult{}, pgmemory.ErrVersionConflict
	}
	rec.Disabled = in.Disabled
	rec.Version++
	f.records[in.MemoryID] = rec
	return corememory.DisableRecordResult{MemoryID: rec.MemoryID, Version: rec.Version, Record: rec}, nil
}

func newPinService(t *testing.T, backend *recordingPinBackend) *Service {
	t.Helper()
	svc, err := New(backend, nil, nil, nil, Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc
}

func TestPinMemory_Idempotent_PinTwice(t *testing.T) {
	backend := newRecordingPinBackend()
	backend.records["mem_pin"] = corememory.MemoryRecord{MemoryID: "mem_pin", Version: 3}
	svc := newPinService(t, backend)

	scope := authz.Scope{TenantID: "tenant", UserID: "user"}
	req := httpapi.PinMemoryRequest{ExpectedVersion: 3}

	first, err := svc.PinMemory(context.Background(), scope, "mem_pin", req)
	if err != nil {
		t.Fatalf("first PinMemory: %v", err)
	}
	if first.Version != 4 || !first.Pinned {
		t.Fatalf("first result = %+v, want Version=4 Pinned=true", first)
	}
	if backend.pinCalls != 1 {
		t.Fatalf("pinCalls after first = %d, want 1", backend.pinCalls)
	}

	// Retry with the stale ExpectedVersion=3 (record now at 4). Already pinned
	// → short-circuit success, backend not called again.
	second, err := svc.PinMemory(context.Background(), scope, "mem_pin", req)
	if err != nil {
		t.Fatalf("second PinMemory: %v", err)
	}
	if second.Version != 4 || !second.Pinned {
		t.Fatalf("second result = %+v, want Version=4 Pinned=true", second)
	}
	if backend.pinCalls != 1 {
		t.Fatalf("pinCalls after replay = %d, want 1 (short-circuited)", backend.pinCalls)
	}
}

func TestPinMemory_Fresh(t *testing.T) {
	backend := newRecordingPinBackend()
	backend.records["mem_pin"] = corememory.MemoryRecord{MemoryID: "mem_pin", Version: 5}
	svc := newPinService(t, backend)

	scope := authz.Scope{TenantID: "tenant", UserID: "user"}
	resp, err := svc.PinMemory(context.Background(), scope, "mem_pin", httpapi.PinMemoryRequest{ExpectedVersion: 5})
	if err != nil {
		t.Fatalf("PinMemory: %v", err)
	}
	if !resp.Pinned || resp.Version != 6 {
		t.Fatalf("resp = %+v, want Pinned=true Version=6", resp)
	}
	if backend.pinCalls != 1 {
		t.Fatalf("pinCalls = %d, want 1", backend.pinCalls)
	}
}

func TestPinMemory_StaleVersionReplay_NoConflict(t *testing.T) {
	backend := newRecordingPinBackend()
	// Already pinned at version 4; client retries with stale ExpectedVersion=3.
	backend.records["mem_pin"] = corememory.MemoryRecord{MemoryID: "mem_pin", Version: 4, Pinned: true}
	svc := newPinService(t, backend)

	scope := authz.Scope{TenantID: "tenant", UserID: "user"}
	resp, err := svc.PinMemory(context.Background(), scope, "mem_pin", httpapi.PinMemoryRequest{ExpectedVersion: 3})
	if err != nil {
		t.Fatalf("PinMemory should not 409 on stale replay: %v", err)
	}
	if !resp.Pinned || resp.Version != 4 {
		t.Fatalf("resp = %+v, want Pinned=true Version=4", resp)
	}
	if backend.pinCalls != 0 {
		t.Fatalf("pinCalls = %d, want 0 (short-circuited)", backend.pinCalls)
	}
}

func TestPinMemory_NotFound(t *testing.T) {
	backend := newRecordingPinBackend()
	svc := newPinService(t, backend)

	scope := authz.Scope{TenantID: "tenant", UserID: "user"}
	_, err := svc.PinMemory(context.Background(), scope, "missing", httpapi.PinMemoryRequest{ExpectedVersion: 1})
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	if got := httpapi.StatusCode(err); got != 404 {
		t.Fatalf("StatusCode(err) = %d, want 404", got)
	}
}

// TestPinMemory_ExactVersion_ShortCircuits pins the `<=` boundary: an already
// pinned record at version 4 with ExpectedVersion=4 must short-circuit (not
// re-run the backend). This guards against a regression flipping `<=` to `<`,
// which would fall through and bump the version to 5.
func TestPinMemory_ExactVersion_ShortCircuits(t *testing.T) {
	backend := newRecordingPinBackend()
	backend.records["mem_pin"] = corememory.MemoryRecord{MemoryID: "mem_pin", Version: 4, Pinned: true}
	svc := newPinService(t, backend)

	scope := authz.Scope{TenantID: "tenant", UserID: "user"}
	resp, err := svc.PinMemory(context.Background(), scope, "mem_pin", httpapi.PinMemoryRequest{ExpectedVersion: 4})
	if err != nil {
		t.Fatalf("PinMemory at exact version should short-circuit: %v", err)
	}
	if !resp.Pinned || resp.Version != 4 {
		t.Fatalf("resp = %+v, want Pinned=true Version=4", resp)
	}
	if backend.pinCalls != 0 {
		t.Fatalf("pinCalls = %d, want 0 (short-circuited at exact version)", backend.pinCalls)
	}
}

// TestPinMemory_ExpectedVersionAhead_Conflict: the record is in the desired
// state but the caller's ExpectedVersion is AHEAD of the stored version, so the
// `<=` guard is false and the request falls through to the backend, which 409s.
// A genuine version disagreement must not be masked as an idempotent success.
func TestPinMemory_ExpectedVersionAhead_Conflict(t *testing.T) {
	backend := newRecordingPinBackend()
	backend.records["mem_pin"] = corememory.MemoryRecord{MemoryID: "mem_pin", Version: 4, Pinned: true}
	svc := newPinService(t, backend)

	scope := authz.Scope{TenantID: "tenant", UserID: "user"}
	_, err := svc.PinMemory(context.Background(), scope, "mem_pin", httpapi.PinMemoryRequest{ExpectedVersion: 5})
	if err == nil {
		t.Fatal("expected conflict when ExpectedVersion is ahead of stored version, got nil")
	}
	if got := httpapi.StatusCode(err); got != 409 {
		t.Fatalf("StatusCode(err) = %d, want 409", got)
	}
}

// TestPinMemory_FreshConflict: a record NOT in the desired state (unpinned)
// with a stale ExpectedVersion must NOT short-circuit and must surface the
// backend's genuine version conflict (409) — the active path is still guarded.
func TestPinMemory_FreshConflict(t *testing.T) {
	backend := newRecordingPinBackend()
	backend.records["mem_pin"] = corememory.MemoryRecord{MemoryID: "mem_pin", Version: 4, Pinned: false}
	svc := newPinService(t, backend)

	scope := authz.Scope{TenantID: "tenant", UserID: "user"}
	_, err := svc.PinMemory(context.Background(), scope, "mem_pin", httpapi.PinMemoryRequest{ExpectedVersion: 3})
	if err == nil {
		t.Fatal("expected conflict on stale ExpectedVersion for a non-pinned record, got nil")
	}
	if got := httpapi.StatusCode(err); got != 409 {
		t.Fatalf("StatusCode(err) = %d, want 409", got)
	}
	if backend.pinCalls != 1 {
		t.Fatalf("pinCalls = %d, want 1 (fell through to the guarded backend)", backend.pinCalls)
	}
}

func TestUnpinMemory_NotFound(t *testing.T) {
	backend := newRecordingPinBackend()
	svc := newPinService(t, backend)

	scope := authz.Scope{TenantID: "tenant", UserID: "user"}
	_, err := svc.UnpinMemory(context.Background(), scope, "missing", httpapi.PinMemoryRequest{ExpectedVersion: 1})
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	if got := httpapi.StatusCode(err); got != 404 {
		t.Fatalf("StatusCode(err) = %d, want 404", got)
	}
}

func TestUnpinMemory_Idempotent_UnpinTwice(t *testing.T) {
	backend := newRecordingPinBackend()
	backend.records["mem_pin"] = corememory.MemoryRecord{MemoryID: "mem_pin", Version: 3, Pinned: true}
	svc := newPinService(t, backend)

	scope := authz.Scope{TenantID: "tenant", UserID: "user"}
	req := httpapi.PinMemoryRequest{ExpectedVersion: 3}

	first, err := svc.UnpinMemory(context.Background(), scope, "mem_pin", req)
	if err != nil {
		t.Fatalf("first UnpinMemory: %v", err)
	}
	if first.Version != 4 || first.Pinned {
		t.Fatalf("first result = %+v, want Version=4 Pinned=false", first)
	}
	if backend.unpinCalls != 1 {
		t.Fatalf("unpinCalls after first = %d, want 1", backend.unpinCalls)
	}

	second, err := svc.UnpinMemory(context.Background(), scope, "mem_pin", req)
	if err != nil {
		t.Fatalf("second UnpinMemory: %v", err)
	}
	if second.Version != 4 || second.Pinned {
		t.Fatalf("second result = %+v, want Version=4 Pinned=false", second)
	}
	if backend.unpinCalls != 1 {
		t.Fatalf("unpinCalls after replay = %d, want 1 (short-circuited)", backend.unpinCalls)
	}
}

func TestUnpinMemory_Fresh(t *testing.T) {
	backend := newRecordingPinBackend()
	backend.records["mem_pin"] = corememory.MemoryRecord{MemoryID: "mem_pin", Version: 5, Pinned: true}
	svc := newPinService(t, backend)

	scope := authz.Scope{TenantID: "tenant", UserID: "user"}
	resp, err := svc.UnpinMemory(context.Background(), scope, "mem_pin", httpapi.PinMemoryRequest{ExpectedVersion: 5})
	if err != nil {
		t.Fatalf("UnpinMemory: %v", err)
	}
	if resp.Pinned || resp.Version != 6 {
		t.Fatalf("resp = %+v, want Pinned=false Version=6", resp)
	}
	if backend.unpinCalls != 1 {
		t.Fatalf("unpinCalls = %d, want 1", backend.unpinCalls)
	}
}

func TestUnpinMemory_StaleVersionReplay_NoConflict(t *testing.T) {
	backend := newRecordingPinBackend()
	// Already unpinned at version 4; client retries with stale ExpectedVersion=3.
	backend.records["mem_pin"] = corememory.MemoryRecord{MemoryID: "mem_pin", Version: 4, Pinned: false}
	svc := newPinService(t, backend)

	scope := authz.Scope{TenantID: "tenant", UserID: "user"}
	resp, err := svc.UnpinMemory(context.Background(), scope, "mem_pin", httpapi.PinMemoryRequest{ExpectedVersion: 3})
	if err != nil {
		t.Fatalf("UnpinMemory should not 409 on stale replay: %v", err)
	}
	if resp.Pinned || resp.Version != 4 {
		t.Fatalf("resp = %+v, want Pinned=false Version=4", resp)
	}
	if backend.unpinCalls != 0 {
		t.Fatalf("unpinCalls = %d, want 0 (short-circuited)", backend.unpinCalls)
	}
}

func TestEnableMemory_Idempotent_AlreadyEnabled(t *testing.T) {
	backend := newRecordingPinBackend()
	// Visible, already enabled (Disabled==false) at version 4; stale retry.
	backend.records["mem_en"] = corememory.MemoryRecord{MemoryID: "mem_en", Version: 4, Disabled: false}
	svc := newPinService(t, backend)

	scope := authz.Scope{TenantID: "tenant", UserID: "user"}
	resp, err := svc.EnableMemory(context.Background(), scope, "mem_en", httpapi.DisableMemoryRequest{ExpectedVersion: 3})
	if err != nil {
		t.Fatalf("EnableMemory should short-circuit: %v", err)
	}
	if resp.Disabled || resp.Version != 4 {
		t.Fatalf("resp = %+v, want Disabled=false Version=4", resp)
	}
	if backend.enableCalls != 0 {
		t.Fatalf("enableCalls = %d, want 0 (short-circuited)", backend.enableCalls)
	}
}

func TestEnableMemory_Disabled_FallsThrough(t *testing.T) {
	backend := newRecordingPinBackend()
	// Disabled record is invisible to GetRecord → no short-circuit → backend runs.
	backend.records["mem_en"] = corememory.MemoryRecord{MemoryID: "mem_en", Version: 5, Disabled: true}
	svc := newPinService(t, backend)

	scope := authz.Scope{TenantID: "tenant", UserID: "user"}
	resp, err := svc.EnableMemory(context.Background(), scope, "mem_en", httpapi.DisableMemoryRequest{ExpectedVersion: 5})
	if err != nil {
		t.Fatalf("EnableMemory: %v", err)
	}
	if resp.Disabled || resp.Version != 6 {
		t.Fatalf("resp = %+v, want Disabled=false Version=6", resp)
	}
	if backend.enableCalls != 1 {
		t.Fatalf("enableCalls = %d, want 1 (fell through)", backend.enableCalls)
	}
}
