package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	"github.com/costa92/llm-agent-memory-gateway/internal/httpapi"
)

// recordingSessionCloser implements SessionCloser and records every invocation
// (call count + the modes it was invoked with, in order).
type recordingSessionCloser struct {
	calls int
	modes []string
	err   error
}

func (c *recordingSessionCloser) CloseSession(_ context.Context, _ authz.Scope, mode string) error {
	c.calls++
	c.modes = append(c.modes, mode)
	return c.err
}

// erroringSessionStateStore fails every LoadSessionState so the CloseSession
// registry-consult error branch can be exercised.
type erroringSessionStateStore struct{ err error }

func (s erroringSessionStateStore) LoadSessionState(context.Context, authz.Scope) (SessionState, bool, error) {
	return SessionState{}, false, s.err
}

func (s erroringSessionStateStore) SaveClosedSession(context.Context, authz.Scope, string, time.Time) (SessionState, error) {
	return SessionState{}, nil
}

func (s erroringSessionStateStore) SaveHeartbeat(context.Context, authz.Scope, time.Time) (SessionState, error) {
	return SessionState{}, nil
}

// countPromoteDecided returns the number of "promote_decided" trace events.
func countPromoteDecided(trace *fakeTraceEmitter) int {
	n := 0
	for _, e := range trace.events {
		if e.stage == "promote_decided" {
			n++
		}
	}
	return n
}

func newCloseTestService(t *testing.T, closer *recordingSessionCloser, trace *fakeTraceEmitter) *Service {
	t.Helper()
	svc, err := New(&fakeBackend{}, nil, closer, trace, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return svc
}

func closeScope() authz.Scope {
	return authz.Scope{
		TenantID:  "tenant-auth",
		UserID:    "user-auth",
		ProjectID: "project-auth",
		SessionID: "session-auth",
	}
}

// TestCloseSession_Idempotent_CloserInvokedOnce asserts that closing the same
// session twice invokes the underlying closer exactly once and emits the
// promote_decided trace exactly once, while both responses reconcile to the
// closed state.
func TestCloseSession_Idempotent_CloserInvokedOnce(t *testing.T) {
	closer := &recordingSessionCloser{}
	trace := &fakeTraceEmitter{}
	svc := newCloseTestService(t, closer, trace)

	scope := closeScope()

	first, err := svc.CloseSession(context.Background(), scope, "session-path", httpapi.SessionCloseRequest{Mode: "expire_working"})
	if err != nil {
		t.Fatalf("first CloseSession() error = %v", err)
	}
	if want := (httpapi.SessionCloseResponse{SessionID: "session-auth", Status: "closed"}); first != want {
		t.Fatalf("first response = %+v, want %+v", first, want)
	}

	second, err := svc.CloseSession(context.Background(), scope, "session-path", httpapi.SessionCloseRequest{Mode: "expire_working"})
	if err != nil {
		t.Fatalf("second CloseSession() error = %v", err)
	}
	if want := (httpapi.SessionCloseResponse{SessionID: "session-auth", Status: "closed"}); second != want {
		t.Fatalf("second response = %+v, want %+v", second, want)
	}

	if closer.calls != 1 {
		t.Fatalf("closer.calls = %d, want 1", closer.calls)
	}
	if got := countPromoteDecided(trace); got != 1 {
		t.Fatalf("promote_decided emissions = %d, want 1", got)
	}
}

// TestCloseSession_Replay_PreservesFirstCloseState asserts first-write-wins on
// mode: a replayed close with a different mode never reaches the closer and the
// response still reflects the closed state.
func TestCloseSession_Replay_PreservesFirstCloseState(t *testing.T) {
	closer := &recordingSessionCloser{}
	trace := &fakeTraceEmitter{}
	svc := newCloseTestService(t, closer, trace)

	scope := closeScope()

	first, err := svc.CloseSession(context.Background(), scope, "session-path", httpapi.SessionCloseRequest{Mode: "promote_and_expire"})
	if err != nil {
		t.Fatalf("first CloseSession() error = %v", err)
	}

	second, err := svc.CloseSession(context.Background(), scope, "session-path", httpapi.SessionCloseRequest{Mode: "expire_working"})
	if err != nil {
		t.Fatalf("second CloseSession() error = %v", err)
	}
	if second.Status != "closed" {
		t.Fatalf("second.Status = %q, want closed", second.Status)
	}
	if second.SessionID != first.SessionID {
		t.Fatalf("second.SessionID = %q, want %q", second.SessionID, first.SessionID)
	}

	if len(closer.modes) != 1 || closer.modes[0] != "promote_and_expire" {
		t.Fatalf("closer.modes = %v, want [promote_and_expire]", closer.modes)
	}
}

// TestHeartbeatSession_ReplayIdempotent characterizes heartbeat idempotency:
// two heartbeats on an active session both report active with no error, and a
// heartbeat after close is rejected with the forbidden "session is closed" error.
func TestHeartbeatSession_ReplayIdempotent(t *testing.T) {
	closer := &recordingSessionCloser{}
	trace := &fakeTraceEmitter{}
	svc := newCloseTestService(t, closer, trace)

	scope := closeScope()

	first, err := svc.HeartbeatSession(context.Background(), scope, "session-path", httpapi.SessionHeartbeatRequest{})
	if err != nil {
		t.Fatalf("first HeartbeatSession() error = %v", err)
	}
	if first.Status != "active" || first.SessionID != "session-auth" {
		t.Fatalf("first response = %+v", first)
	}

	second, err := svc.HeartbeatSession(context.Background(), scope, "session-path", httpapi.SessionHeartbeatRequest{})
	if err != nil {
		t.Fatalf("second HeartbeatSession() error = %v", err)
	}
	if second.Status != "active" || second.SessionID != "session-auth" {
		t.Fatalf("second response = %+v", second)
	}

	if _, err := svc.CloseSession(context.Background(), scope, "session-path", httpapi.SessionCloseRequest{Mode: "expire_working"}); err != nil {
		t.Fatalf("CloseSession() error = %v", err)
	}

	_, err = svc.HeartbeatSession(context.Background(), scope, "session-path", httpapi.SessionHeartbeatRequest{})
	if err == nil {
		t.Fatal("expected forbidden error on heartbeat after close, got nil")
	}
	if got := httpapi.StatusCode(err); got != 403 {
		t.Fatalf("StatusCode(err) = %d, want 403", got)
	}
}

// TestCloseSession_FirstClose_InvokesCloserAndTrace guards that reordering the
// registry consult before the closer call did not drop the first-close side
// effects: the closer runs once and the promote_decided trace is emitted once.
func TestCloseSession_FirstClose_InvokesCloserAndTrace(t *testing.T) {
	closer := &recordingSessionCloser{}
	trace := &fakeTraceEmitter{}
	svc := newCloseTestService(t, closer, trace)

	resp, err := svc.CloseSession(context.Background(), closeScope(), "session-path", httpapi.SessionCloseRequest{Mode: "expire_working"})
	if err != nil {
		t.Fatalf("CloseSession() error = %v", err)
	}
	if resp.Status != "closed" {
		t.Fatalf("resp.Status = %q, want closed", resp.Status)
	}
	if closer.calls != 1 {
		t.Fatalf("closer.calls = %d, want 1", closer.calls)
	}
	if got := countPromoteDecided(trace); got != 1 {
		t.Fatalf("promote_decided emissions = %d, want 1", got)
	}
}

// TestCloseSession_GetError_Propagates asserts that a failure from the registry
// consult (the new idempotency short-circuit read) is surfaced to the caller
// rather than swallowed, and that the closer is not invoked in that case.
func TestCloseSession_GetError_Propagates(t *testing.T) {
	closer := &recordingSessionCloser{}
	svc, err := New(&fakeBackend{}, nil, closer, &fakeTraceEmitter{}, Config{
		SessionStateStore: erroringSessionStateStore{err: errors.New("registry boom")},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = svc.CloseSession(context.Background(), closeScope(), "session-path", httpapi.SessionCloseRequest{Mode: "expire_working"})
	if err == nil {
		t.Fatal("expected error when registry consult fails, got nil")
	}
	if closer.calls != 0 {
		t.Fatalf("closer.calls = %d, want 0 (closer must not run when the consult fails)", closer.calls)
	}
}

// TestCloseSession_CloserError_Propagates asserts that an error from the
// underlying closer on the first (not-yet-closed) close is surfaced.
func TestCloseSession_CloserError_Propagates(t *testing.T) {
	closer := &recordingSessionCloser{err: errors.New("closer boom")}
	svc := newCloseTestService(t, closer, &fakeTraceEmitter{})

	_, err := svc.CloseSession(context.Background(), closeScope(), "session-path", httpapi.SessionCloseRequest{Mode: "expire_working"})
	if err == nil {
		t.Fatal("expected error when closer fails, got nil")
	}
	if closer.calls != 1 {
		t.Fatalf("closer.calls = %d, want 1", closer.calls)
	}
}
