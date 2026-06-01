package service

import (
	"context"
	"time"
)

// StorageRow is the per-tenant aggregate row returned by the cron's injected
// query function. TenantID is the raw tenant identifier — the cron hashes it
// through tenantBucket(...) before publishing to the metrics sink so the
// cardinality discipline lives in one place.
type StorageRow struct {
	TenantID    string
	MemoryBytes uint64
	VectorBytes uint64
}

// StorageMetricsSink is the surface the cron writes into. *observability.Metrics
// satisfies this interface; the indirection breaks the import cycle that would
// otherwise arise (observability already imports service). The wiring lives in
// cmd/memory-gateway/main.go.
type StorageMetricsSink interface {
	SetMemoryStorageBytes(tenantBucket string, b uint64)
	SetVectorStorageBytes(tenantBucket string, b uint64)
	AddStorageCronFailure()
}

// StorageMetricsCronConfig wires the cron's dependencies. Query and Metrics are
// required; Interval defaults to 5 minutes if zero (matches the M7 spec
// default — operators override via env).
type StorageMetricsCronConfig struct {
	Query    func(ctx context.Context) ([]StorageRow, error)
	Metrics  StorageMetricsSink
	Interval time.Duration
	// Now is a clock injection point for tests. Defaults to time.Now.
	Now func() time.Time
}

// StorageMetricsCron periodically queries per-tenant storage byte counts and
// publishes them to the metrics sink. On query failure it bumps the failure
// counter and skips the tick — subsequent ticks still run, so a transient DB
// hiccup doesn't permanently stall the metric.
type StorageMetricsCron struct {
	query    func(ctx context.Context) ([]StorageRow, error)
	metrics  StorageMetricsSink
	interval time.Duration
	now      func() time.Time

	stop chan struct{}
	done chan struct{}
}

// NewStorageMetricsCron constructs the cron. It does not start the loop —
// call Run(ctx) (typically in a goroutine) once construction is wired into
// the lifecycle.
func NewStorageMetricsCron(cfg StorageMetricsCronConfig) *StorageMetricsCron {
	interval := cfg.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &StorageMetricsCron{
		query:    cfg.Query,
		metrics:  cfg.Metrics,
		interval: interval,
		now:      now,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Run drives the ticker loop. Returns when ctx is cancelled or Stop is called.
// Safe to call at most once per cron instance.
func (c *StorageMetricsCron) Run(ctx context.Context) {
	defer close(c.done)
	if c.query == nil || c.metrics == nil {
		return
	}

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stop:
			return
		case <-ticker.C:
			c.runOnce(ctx)
		}
	}
}

// Stop signals the loop to exit and blocks until it does (bounded by ctx if
// the caller provides one with a deadline). Idempotent.
func (c *StorageMetricsCron) Stop(ctx context.Context) {
	select {
	case <-c.stop:
		// already stopped
	default:
		close(c.stop)
	}
	select {
	case <-c.done:
	case <-ctx.Done():
	}
}

// runOnce performs a single query+publish cycle. Query failure increments the
// failure counter and returns without touching the storage metrics — the
// previous gauge values stay until the next successful tick.
func (c *StorageMetricsCron) runOnce(ctx context.Context) {
	rows, err := c.query(ctx)
	if err != nil {
		c.metrics.AddStorageCronFailure()
		return
	}

	// Aggregate to tenant_bucket. Postgres typically pre-aggregates with
	// GROUP BY tenant_id, but we sum within bucket in Go so the cron remains
	// correct even if the query returns duplicate tenant rows.
	type aggregated struct {
		memory uint64
		vector uint64
	}
	byBucket := make(map[string]aggregated, len(rows))
	for _, row := range rows {
		bucket := tenantBucket(row.TenantID)
		a := byBucket[bucket]
		a.memory += row.MemoryBytes
		a.vector += row.VectorBytes
		byBucket[bucket] = a
	}
	for bucket, a := range byBucket {
		c.metrics.SetMemoryStorageBytes(bucket, a.memory)
		c.metrics.SetVectorStorageBytes(bucket, a.vector)
	}
}
