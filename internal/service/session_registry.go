package service

import (
	"context"
	"sync"
	"time"

	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
)

type SessionState struct {
	TenantID        string
	UserID          string
	ProjectID       string
	SessionID       string
	Status          string
	Mode            string
	ClosedAt        time.Time
	LastHeartbeatAt time.Time
}

type SessionStateStore interface {
	LoadSessionState(ctx context.Context, scope authz.Scope) (SessionState, bool, error)
	SaveClosedSession(ctx context.Context, scope authz.Scope, mode string, now time.Time) (SessionState, error)
	SaveHeartbeat(ctx context.Context, scope authz.Scope, now time.Time) (SessionState, error)
}

type sessionRegistry struct {
	store SessionStateStore
	mu    sync.RWMutex
	cache map[string]SessionState
}

func newSessionRegistry(store SessionStateStore) *sessionRegistry {
	if store == nil {
		store = newMemorySessionStateStore()
	}
	return &sessionRegistry{
		store: store,
		cache: map[string]SessionState{},
	}
}

func (r *sessionRegistry) Get(ctx context.Context, scope authz.Scope) (SessionState, bool, error) {
	key := sessionScopeKey(scope)

	r.mu.RLock()
	if state, ok := r.cache[key]; ok {
		r.mu.RUnlock()
		return state, true, nil
	}
	r.mu.RUnlock()

	state, ok, err := r.store.LoadSessionState(ctx, scope)
	if err != nil || !ok {
		return state, ok, err
	}

	r.mu.Lock()
	r.cache[key] = state
	r.mu.Unlock()
	return state, true, nil
}

func (r *sessionRegistry) Close(ctx context.Context, scope authz.Scope, mode string, now time.Time) (SessionState, error) {
	state, err := r.store.SaveClosedSession(ctx, scope, mode, now)
	if err != nil {
		return SessionState{}, err
	}

	r.mu.Lock()
	r.cache[sessionScopeKey(scope)] = state
	r.mu.Unlock()
	return state, nil
}

func (r *sessionRegistry) Heartbeat(ctx context.Context, scope authz.Scope, now time.Time) (SessionState, error) {
	state, err := r.store.SaveHeartbeat(ctx, scope, now)
	if err != nil {
		return SessionState{}, err
	}

	r.mu.Lock()
	r.cache[sessionScopeKey(scope)] = state
	r.mu.Unlock()
	return state, nil
}

type memorySessionStateStore struct {
	mu      sync.RWMutex
	entries map[string]SessionState
}

func newMemorySessionStateStore() *memorySessionStateStore {
	return &memorySessionStateStore{entries: map[string]SessionState{}}
}

func (s *memorySessionStateStore) LoadSessionState(_ context.Context, scope authz.Scope) (SessionState, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.entries[sessionScopeKey(scope)]
	return state, ok, nil
}

func (s *memorySessionStateStore) SaveClosedSession(_ context.Context, scope authz.Scope, mode string, now time.Time) (SessionState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := sessionScopeKey(scope)
	if existing, ok := s.entries[key]; ok && existing.Status == "closed" {
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
	if existing, ok := s.entries[key]; ok {
		state.LastHeartbeatAt = existing.LastHeartbeatAt
	}
	s.entries[key] = state
	return state, nil
}

func (s *memorySessionStateStore) SaveHeartbeat(_ context.Context, scope authz.Scope, now time.Time) (SessionState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := sessionScopeKey(scope)
	if existing, ok := s.entries[key]; ok && existing.Status == "closed" {
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
	s.entries[key] = state
	return state, nil
}

func sessionScopeKey(scope authz.Scope) string {
	return scope.TenantID + "|" + scope.UserID + "|" + scope.ProjectID + "|" + scope.SessionID
}
