package service

import "context"

type OutboxProjectionObservation struct {
	Status         string
	EventType      string
	TenantID       string
	MemoryID       string
	EventVersion   int64
	CurrentVersion int64
	Reason         string
}

type OutboxProjectionObserver interface {
	ObserveProjection(ctx context.Context, obs OutboxProjectionObservation)
}

type OutboxProjectionCounters struct {
	Projected int
	Stale     int
	Failed    int
	Ignored   int
}

func (c *OutboxProjectionCounters) ObserveProjection(_ context.Context, obs OutboxProjectionObservation) {
	switch obs.Status {
	case "projected":
		c.Projected++
	case "stale":
		c.Stale++
	case "failed":
		c.Failed++
	case "ignored":
		c.Ignored++
	}
}

type nopOutboxProjectionObserver struct{}

func (nopOutboxProjectionObserver) ObserveProjection(context.Context, OutboxProjectionObservation) {}

func resolveOutboxProjectionObserver(observer OutboxProjectionObserver) OutboxProjectionObserver {
	if observer != nil {
		return observer
	}
	return nopOutboxProjectionObserver{}
}
