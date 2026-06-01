package service

import (
	"sync"
	"time"

	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	"github.com/costa92/llm-agent-memory-gateway/internal/httpapi"
)

const (
	eventualCacheTTL = 30 * time.Second
	boundedCacheTTL  = 5 * time.Second
)

type recallCacheEntry struct {
	response     httpapi.RecallUnifiedResponse
	cachedAt     time.Time
	scopeVersion int64
}

type recallCache struct {
	mu      sync.RWMutex
	entries map[string]recallCacheEntry
}

func newRecallCache() *recallCache {
	return &recallCache{entries: map[string]recallCacheEntry{}}
}

func (c *recallCache) Get(key string) (recallCacheEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[key]
	return entry, ok
}

func (c *recallCache) Set(key string, resp httpapi.RecallUnifiedResponse, cachedAt time.Time, scopeVersion int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = recallCacheEntry{
		response:     resp,
		cachedAt:     cachedAt,
		scopeVersion: scopeVersion,
	}
}

func (c *recallCache) InvalidateScope(scope authz.Scope) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.entries {
		if hasScopePrefix(key, scope) {
			delete(c.entries, key)
		}
	}
}

func (c *recallCache) Lookup(key, consistencyLevel string, allowStale bool, now time.Time, currentScopeVersion int64) (httpapi.RecallUnifiedResponse, bool, bool) {
	entry, ok := c.Get(key)
	if !ok {
		return httpapi.RecallUnifiedResponse{}, false, false
	}

	age := now.Sub(entry.cachedAt)
	switch consistencyLevel {
	case "bounded":
		if entry.scopeVersion != currentScopeVersion {
			return httpapi.RecallUnifiedResponse{}, false, false
		}
		if age > boundedCacheTTL {
			return httpapi.RecallUnifiedResponse{}, false, false
		}
		return entry.response, true, false
	case "eventual":
		if age <= eventualCacheTTL {
			return entry.response, true, false
		}
		if allowStale {
			return entry.response, true, true
		}
		return httpapi.RecallUnifiedResponse{}, false, false
	default:
		return entry.response, true, false
	}
}
