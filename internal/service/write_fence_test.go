package service

import (
	"context"
	"testing"
	"time"

	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	"github.com/costa92/llm-agent-memory-gateway/internal/httpapi"
)

// newFenceTestService builds a Service with fakes and an in-memory session
// registry (the default when no SessionStateStore is configured).
func newFenceTestService(t *testing.T) *Service {
	t.Helper()
	svc, err := New(&fakeBackend{}, &fakeRecaller{}, &fakeSessionCloser{}, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return svc
}

func closedScope(t *testing.T, svc *Service) authz.Scope {
	t.Helper()
	scope := authz.Scope{TenantID: "t", UserID: "u", ProjectID: "p", SessionID: "s-closed"}
	if _, err := svc.CloseSession(context.Background(), scope, scope.SessionID, httpapi.SessionCloseRequest{Mode: "expire_working"}); err != nil {
		t.Fatalf("CloseSession() error = %v", err)
	}
	return scope
}

// D8: every mutator must reject a write whose scope targets a closed session.
func TestMutators_RejectedOnClosedSession(t *testing.T) {
	ctx := context.Background()

	t.Run("WriteMemory", func(t *testing.T) {
		svc := newFenceTestService(t)
		scope := closedScope(t, svc)
		_, err := svc.WriteMemory(ctx, scope, httpapi.WriteMemoryRequest{
			IdempotencyKey: "k1",
			Record:         httpapi.WriteRecordPayload{Source: "user_saved", Content: "x"},
		})
		assertForbidden(t, err)
	})

	t.Run("PatchMemory", func(t *testing.T) {
		svc := newFenceTestService(t)
		scope := closedScope(t, svc)
		_, err := svc.PatchMemory(ctx, scope, "m1", httpapi.PatchMemoryRequest{ExpectedVersion: 1})
		assertForbidden(t, err)
	})

	t.Run("PinMemory", func(t *testing.T) {
		svc := newFenceTestService(t)
		scope := closedScope(t, svc)
		_, err := svc.PinMemory(ctx, scope, "m1", httpapi.PinMemoryRequest{ExpectedVersion: 1})
		assertForbidden(t, err)
	})

	t.Run("DisableMemory", func(t *testing.T) {
		svc := newFenceTestService(t)
		scope := closedScope(t, svc)
		_, err := svc.DisableMemory(ctx, scope, "m1", httpapi.DisableMemoryRequest{ExpectedVersion: 1})
		assertForbidden(t, err)
	})

	t.Run("DeleteMemory", func(t *testing.T) {
		svc := newFenceTestService(t)
		scope := closedScope(t, svc)
		_, err := svc.DeleteMemory(ctx, scope, "m1", httpapi.DeleteMemoryRequest{ExpectedVersion: 1})
		assertForbidden(t, err)
	})
}

// A write with no registered session (the common case) must NOT be fenced.
func TestWriteMemory_SessionlessWriteAllowed(t *testing.T) {
	svc := newFenceTestService(t)
	scope := authz.Scope{TenantID: "t", UserID: "u", ProjectID: "p"} // no SessionID
	if _, err := svc.WriteMemory(context.Background(), scope, httpapi.WriteMemoryRequest{
		IdempotencyKey: "k1",
		Record:         httpapi.WriteRecordPayload{Source: "user_saved", Content: "x"},
	}); err != nil {
		t.Fatalf("sessionless write should be allowed, got %v", err)
	}
}

// A write to an active (open) session must NOT be fenced.
func TestWriteMemory_ActiveSessionWriteAllowed(t *testing.T) {
	svc := newFenceTestService(t)
	scope := authz.Scope{TenantID: "t", UserID: "u", ProjectID: "p", SessionID: "s-open"}
	if _, err := svc.sessionRegistry.Heartbeat(context.Background(), scope, time.Now().UTC()); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	if _, err := svc.WriteMemory(context.Background(), scope, httpapi.WriteMemoryRequest{
		IdempotencyKey: "k1",
		Record:         httpapi.WriteRecordPayload{Source: "user_saved", Content: "x"},
	}); err != nil {
		t.Fatalf("active-session write should be allowed, got %v", err)
	}
}

func assertForbidden(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected closed-session mutation to be rejected, got nil")
	}
	if got := httpapi.StatusCode(err); got != 403 {
		t.Fatalf("StatusCode(err) = %d, want 403 (session is closed)", got)
	}
}
