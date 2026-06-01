package service

import "context"

type RecallObservation struct {
	ConsistencyLevel string
	CacheLevel       string
	StaleServed      bool

	// TenantID is the tenant whose recall produced this observation. The
	// metrics observer buckets it before emitting (see Open Decisions /
	// Cardinality in the M7 plan). Empty string is tolerated and falls into
	// the "unknown" bucket.
	TenantID string

	// Returned is the count of records returned by the recall backend before
	// post-recall budget filtering. Zero on the cache-hit path (no fresh
	// recall happened), so the metrics observer skips the increment.
	Returned int

	// Selected is the count of records that survived post-recall budget
	// filtering and made it into the response. Zero on the cache-hit path.
	Selected int
}

type RecallObserver interface {
	ObserveRecall(ctx context.Context, obs RecallObservation)
}

type nopRecallObserver struct{}

func (nopRecallObserver) ObserveRecall(context.Context, RecallObservation) {}

func resolveRecallObserver(observer RecallObserver) RecallObserver {
	if observer != nil {
		return observer
	}
	return nopRecallObserver{}
}
