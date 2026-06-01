package service

import (
	"context"

	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
)

type RecallCacheObservation struct {
	Action string
	Scope  authz.Scope
}

type RecallCacheObserver interface {
	ObserveRecallCache(ctx context.Context, obs RecallCacheObservation)
}

type nopRecallCacheObserver struct{}

func (nopRecallCacheObserver) ObserveRecallCache(context.Context, RecallCacheObservation) {}

func resolveRecallCacheObserver(observer RecallCacheObserver) RecallCacheObserver {
	if observer != nil {
		return observer
	}
	return nopRecallCacheObserver{}
}
