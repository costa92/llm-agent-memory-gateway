package service

import (
	"context"
	"testing"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	"github.com/costa92/llm-agent-memory-gateway/internal/httpapi"
)

// These tests drive the hidden-state short-circuit added to DisableMemory and
// DeleteMemory. recordingPinBackend's GetRecordIncludingHidden returns
// soft-deleted/disabled rows (unlike GetRecord, which hides them), so a replay
// after a successful disable/delete reconciles to an idempotent success instead
// of a 409.

func TestDeleteMemory_Idempotent_DeleteTwice(t *testing.T) {
	backend := newRecordingPinBackend()
	backend.records["mem_del"] = corememory.MemoryRecord{MemoryID: "mem_del", Version: 3}
	svc := newPinService(t, backend)

	scope := authz.Scope{TenantID: "tenant", UserID: "user"}
	req := httpapi.DeleteMemoryRequest{ExpectedVersion: 3}

	first, err := svc.DeleteMemory(context.Background(), scope, "mem_del", req)
	if err != nil {
		t.Fatalf("first DeleteMemory: %v", err)
	}
	if !first.Deleted || first.Version != 4 {
		t.Fatalf("first result = %+v, want Deleted=true Version=4", first)
	}
	if backend.deleteCalls != 1 {
		t.Fatalf("deleteCalls after first = %d, want 1", backend.deleteCalls)
	}

	// Replay with the same stale ExpectedVersion=3 (record now deleted at 4).
	// Short-circuit success, backend not called again, version stable.
	second, err := svc.DeleteMemory(context.Background(), scope, "mem_del", req)
	if err != nil {
		t.Fatalf("second DeleteMemory: %v", err)
	}
	if !second.Deleted || second.Version != 4 {
		t.Fatalf("second result = %+v, want Deleted=true Version=4", second)
	}
	if backend.deleteCalls != 1 {
		t.Fatalf("deleteCalls after replay = %d, want 1 (short-circuited)", backend.deleteCalls)
	}
}

func TestDeleteMemory_StaleVersionReplay_NoConflict(t *testing.T) {
	backend := newRecordingPinBackend()
	// Already deleted at version 4; caller retries with stale ExpectedVersion=3.
	backend.records["mem_del"] = corememory.MemoryRecord{MemoryID: "mem_del", Version: 4, Deleted: true}
	svc := newPinService(t, backend)

	scope := authz.Scope{TenantID: "tenant", UserID: "user"}
	resp, err := svc.DeleteMemory(context.Background(), scope, "mem_del", httpapi.DeleteMemoryRequest{ExpectedVersion: 3})
	if err != nil {
		t.Fatalf("DeleteMemory should not 409 on stale replay: %v", err)
	}
	if !resp.Deleted || resp.Version != 4 {
		t.Fatalf("resp = %+v, want Deleted=true Version=4", resp)
	}
	if backend.deleteCalls != 0 {
		t.Fatalf("deleteCalls = %d, want 0 (short-circuited)", backend.deleteCalls)
	}
}

func TestDeleteMemory_TrulyAbsent_NotFound(t *testing.T) {
	backend := newRecordingPinBackend()
	svc := newPinService(t, backend)

	scope := authz.Scope{TenantID: "tenant", UserID: "user"}
	_, err := svc.DeleteMemory(context.Background(), scope, "missing", httpapi.DeleteMemoryRequest{ExpectedVersion: 1})
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	if got := httpapi.StatusCode(err); got != 404 {
		t.Fatalf("StatusCode(err) = %d, want 404", got)
	}
}

func TestDeleteMemory_GenuineConflict_CallerAhead(t *testing.T) {
	backend := newRecordingPinBackend()
	// Not yet deleted at version 4; caller's ExpectedVersion is ahead.
	backend.records["mem_del"] = corememory.MemoryRecord{MemoryID: "mem_del", Version: 4}
	svc := newPinService(t, backend)

	scope := authz.Scope{TenantID: "tenant", UserID: "user"}
	_, err := svc.DeleteMemory(context.Background(), scope, "mem_del", httpapi.DeleteMemoryRequest{ExpectedVersion: 5})
	if err == nil {
		t.Fatal("expected conflict when caller is ahead, got nil")
	}
	if got := httpapi.StatusCode(err); got != 409 {
		t.Fatalf("StatusCode(err) = %d, want 409", got)
	}
}

func TestDisableMemory_Idempotent_DisableTwice(t *testing.T) {
	backend := newRecordingPinBackend()
	backend.records["mem_dis"] = corememory.MemoryRecord{MemoryID: "mem_dis", Version: 3}
	svc := newPinService(t, backend)

	scope := authz.Scope{TenantID: "tenant", UserID: "user"}
	req := httpapi.DisableMemoryRequest{ExpectedVersion: 3}

	first, err := svc.DisableMemory(context.Background(), scope, "mem_dis", req)
	if err != nil {
		t.Fatalf("first DisableMemory: %v", err)
	}
	if !first.Disabled || first.Version != 4 {
		t.Fatalf("first result = %+v, want Disabled=true Version=4", first)
	}
	if backend.disableCalls != 1 {
		t.Fatalf("disableCalls after first = %d, want 1", backend.disableCalls)
	}

	second, err := svc.DisableMemory(context.Background(), scope, "mem_dis", req)
	if err != nil {
		t.Fatalf("second DisableMemory: %v", err)
	}
	if !second.Disabled || second.Version != 4 {
		t.Fatalf("second result = %+v, want Disabled=true Version=4", second)
	}
	if backend.disableCalls != 1 {
		t.Fatalf("disableCalls after replay = %d, want 1 (short-circuited)", backend.disableCalls)
	}
}

func TestDisableMemory_StaleVersionReplay_NoConflict(t *testing.T) {
	backend := newRecordingPinBackend()
	backend.records["mem_dis"] = corememory.MemoryRecord{MemoryID: "mem_dis", Version: 4, Disabled: true}
	svc := newPinService(t, backend)

	scope := authz.Scope{TenantID: "tenant", UserID: "user"}
	resp, err := svc.DisableMemory(context.Background(), scope, "mem_dis", httpapi.DisableMemoryRequest{ExpectedVersion: 3})
	if err != nil {
		t.Fatalf("DisableMemory should not 409 on stale replay: %v", err)
	}
	if !resp.Disabled || resp.Version != 4 {
		t.Fatalf("resp = %+v, want Disabled=true Version=4", resp)
	}
	if backend.disableCalls != 0 {
		t.Fatalf("disableCalls = %d, want 0 (short-circuited)", backend.disableCalls)
	}
}

func TestDisableMemory_TrulyAbsent_NotFound(t *testing.T) {
	backend := newRecordingPinBackend()
	svc := newPinService(t, backend)

	scope := authz.Scope{TenantID: "tenant", UserID: "user"}
	_, err := svc.DisableMemory(context.Background(), scope, "missing", httpapi.DisableMemoryRequest{ExpectedVersion: 1})
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	if got := httpapi.StatusCode(err); got != 404 {
		t.Fatalf("StatusCode(err) = %d, want 404", got)
	}
}

func TestDisableMemory_GenuineConflict_CallerAhead(t *testing.T) {
	backend := newRecordingPinBackend()
	backend.records["mem_dis"] = corememory.MemoryRecord{MemoryID: "mem_dis", Version: 4}
	svc := newPinService(t, backend)

	scope := authz.Scope{TenantID: "tenant", UserID: "user"}
	_, err := svc.DisableMemory(context.Background(), scope, "mem_dis", httpapi.DisableMemoryRequest{ExpectedVersion: 5})
	if err == nil {
		t.Fatal("expected conflict when caller is ahead, got nil")
	}
	if got := httpapi.StatusCode(err); got != 409 {
		t.Fatalf("StatusCode(err) = %d, want 409", got)
	}
}

// TestDisableMemory_OnDeletedRow_FallsThrough is the codex-refined edge case: a
// record that is BOTH Deleted and Disabled must NOT short-circuit a disable
// replay (predicate Disabled && !Deleted is false). It falls through to the
// backend mutation path rather than reporting a clean disable replay. Here the
// record sits at version 4 while the caller sends a stale ExpectedVersion=3, so
// the fall-through backend mutation surfaces a 409 conflict.
func TestDisableMemory_OnDeletedRow_FallsThrough(t *testing.T) {
	backend := newRecordingPinBackend()
	backend.records["mem_dd"] = corememory.MemoryRecord{MemoryID: "mem_dd", Version: 4, Deleted: true, Disabled: true}
	svc := newPinService(t, backend)

	scope := authz.Scope{TenantID: "tenant", UserID: "user"}
	_, err := svc.DisableMemory(context.Background(), scope, "mem_dd", httpapi.DisableMemoryRequest{ExpectedVersion: 3})
	if err == nil {
		t.Fatal("expected fall-through to backend (no short-circuit on deleted+disabled), got nil")
	}
	if got := httpapi.StatusCode(err); got != 409 {
		t.Fatalf("StatusCode(err) = %d, want 409 (fell through to guarded backend)", got)
	}
	if backend.disableCalls != 1 {
		t.Fatalf("disableCalls = %d, want 1 (fell through, did not short-circuit)", backend.disableCalls)
	}
}
