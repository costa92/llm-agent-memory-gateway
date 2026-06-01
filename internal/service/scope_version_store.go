package service

import (
	"context"
	"sync"

	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
)

type ScopeVersionStore interface {
	CurrentScopeVersion(ctx context.Context, scope authz.Scope) (int64, error)
	BumpScopeVersion(ctx context.Context, scope authz.Scope) (int64, error)
}

type memoryScopeVersionStore struct {
	mu       sync.RWMutex
	versions map[string]int64
}

func newMemoryScopeVersionStore() *memoryScopeVersionStore {
	return &memoryScopeVersionStore{versions: map[string]int64{}}
}

func (s *memoryScopeVersionStore) CurrentScopeVersion(_ context.Context, scope authz.Scope) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.versions[sessionScopeKey(scope)], nil
}

func (s *memoryScopeVersionStore) BumpScopeVersion(_ context.Context, scope authz.Scope) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionScopeKey(scope)
	s.versions[key]++
	return s.versions[key], nil
}
