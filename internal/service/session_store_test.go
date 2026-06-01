package service

import (
	"context"
	"testing"
	"time"

	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
)

type fakeSessionStateStore struct {
	states map[string]SessionState
}

func newFakeSessionStateStore() *fakeSessionStateStore {
	return &fakeSessionStateStore{states: map[string]SessionState{}}
}

func (s *fakeSessionStateStore) LoadSessionState(_ context.Context, scope authz.Scope) (SessionState, bool, error) {
	state, ok := s.states[sessionScopeKey(scope)]
	return state, ok, nil
}

func (s *fakeSessionStateStore) SaveClosedSession(_ context.Context, scope authz.Scope, mode string, now time.Time) (SessionState, error) {
	key := sessionScopeKey(scope)
	if existing, ok := s.states[key]; ok && existing.Status == "closed" {
		return existing, nil
	}
	state := SessionState{
		TenantID:  scope.TenantID,
		UserID:    scope.UserID,
		ProjectID: scope.ProjectID,
		SessionID: scope.SessionID,
		Status:    "closed",
		Mode:      mode,
		ClosedAt:  now,
	}
	if existing, ok := s.states[key]; ok {
		state.LastHeartbeatAt = existing.LastHeartbeatAt
	}
	s.states[key] = state
	return state, nil
}

func (s *fakeSessionStateStore) SaveHeartbeat(_ context.Context, scope authz.Scope, now time.Time) (SessionState, error) {
	key := sessionScopeKey(scope)
	if existing, ok := s.states[key]; ok && existing.Status == "closed" {
		return existing, nil
	}
	state := SessionState{
		TenantID:        scope.TenantID,
		UserID:          scope.UserID,
		ProjectID:       scope.ProjectID,
		SessionID:       scope.SessionID,
		Status:          "active",
		LastHeartbeatAt: now,
	}
	s.states[key] = state
	return state, nil
}

func TestSessionRegistry_UsesBackingStoreAcrossServiceInstances(t *testing.T) {
	store := newFakeSessionStateStore()
	registryA := newSessionRegistry(store)
	registryB := newSessionRegistry(store)
	scope := authz.Scope{
		TenantID:  "tenant-a",
		UserID:    "user-1",
		ProjectID: "proj-x",
		SessionID: "sess-9",
	}

	closed, err := registryA.Close(context.Background(), scope, "expire_working", time.Now().UTC())
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	got, ok, err := registryB.Get(context.Background(), scope)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("expected session state from backing store")
	}
	if got.Status != "closed" || got.SessionID != closed.SessionID {
		t.Fatalf("got = %+v", got)
	}
}

func TestSessionRegistry_CloseIsIdempotentAcrossInstances(t *testing.T) {
	store := newFakeSessionStateStore()
	registryA := newSessionRegistry(store)
	registryB := newSessionRegistry(store)
	scope := authz.Scope{
		TenantID:  "tenant-a",
		UserID:    "user-1",
		ProjectID: "proj-x",
		SessionID: "sess-9",
	}

	first, err := registryA.Close(context.Background(), scope, "promote_and_expire", time.Now().UTC())
	if err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	second, err := registryB.Close(context.Background(), scope, "promote_and_expire", time.Now().UTC().Add(time.Minute))
	if err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if !first.ClosedAt.Equal(second.ClosedAt) {
		t.Fatalf("ClosedAt differs: %v vs %v", first.ClosedAt, second.ClosedAt)
	}
}

func TestSessionRegistry_CloseTransitionsActiveSessionToClosed(t *testing.T) {
	store := newFakeSessionStateStore()
	registry := newSessionRegistry(store)
	scope := authz.Scope{
		TenantID:  "tenant-a",
		UserID:    "user-1",
		ProjectID: "proj-x",
		SessionID: "sess-9",
	}

	heartbeatAt := time.Now().UTC().Add(-time.Minute)
	active, err := registry.Heartbeat(context.Background(), scope, heartbeatAt)
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	if active.Status != "active" {
		t.Fatalf("active state = %+v", active)
	}

	closedAt := time.Now().UTC()
	closed, err := registry.Close(context.Background(), scope, "expire_working", closedAt)
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if closed.Status != "closed" {
		t.Fatalf("closed state = %+v", closed)
	}
	if !closed.ClosedAt.Equal(closedAt) {
		t.Fatalf("ClosedAt = %v, want %v", closed.ClosedAt, closedAt)
	}
	if !closed.LastHeartbeatAt.Equal(heartbeatAt) {
		t.Fatalf("LastHeartbeatAt = %v, want %v", closed.LastHeartbeatAt, heartbeatAt)
	}
}

func TestSessionRegistry_HeartbeatDoesNotSetClosedAt(t *testing.T) {
	store := newFakeSessionStateStore()
	registry := newSessionRegistry(store)
	scope := authz.Scope{
		TenantID:  "tenant-a",
		UserID:    "user-1",
		ProjectID: "proj-x",
		SessionID: "sess-9",
	}

	now := time.Now().UTC()
	state, err := registry.Heartbeat(context.Background(), scope, now)
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	if !state.ClosedAt.IsZero() {
		t.Fatalf("ClosedAt = %v, want zero", state.ClosedAt)
	}
	if !state.LastHeartbeatAt.Equal(now) {
		t.Fatalf("LastHeartbeatAt = %v, want %v", state.LastHeartbeatAt, now)
	}
}

type fakeScopeVersionStore struct {
	versions map[string]int64
}

func newFakeScopeVersionStore() *fakeScopeVersionStore {
	return &fakeScopeVersionStore{versions: map[string]int64{}}
}

func (s *fakeScopeVersionStore) CurrentScopeVersion(_ context.Context, scope authz.Scope) (int64, error) {
	return s.versions[sessionScopeKey(scope)], nil
}

func (s *fakeScopeVersionStore) BumpScopeVersion(_ context.Context, scope authz.Scope) (int64, error) {
	key := sessionScopeKey(scope)
	s.versions[key]++
	return s.versions[key], nil
}
