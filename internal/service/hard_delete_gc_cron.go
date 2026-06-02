package service

import (
	"context"
	"time"
)

// HardDeleteGCCronConfig wires the hard-delete GC (D4): a background job that
// physically removes memory records that have been soft-deleted (deleted=TRUE)
// for longer than Retention. Soft-deleted rows are already invisible to every
// query, so this only reclaims storage — it never changes behaviour. The
// retention window is a safety margin against an accidental delete being
// physically gone before anyone notices.
//
// Purge is required and returns the number of rows removed. memory_event /
// outbox rows are independent (no FK to memory_record) and are intentionally
// left as history.
type HardDeleteGCCronConfig struct {
	Purge func(ctx context.Context, cutoff time.Time) (int64, error)
	// Retention is how long a soft-deleted row is kept before physical removal.
	// Defaults to 30 days.
	Retention time.Duration
	// Interval is how often the GC runs. Defaults to 1h.
	Interval time.Duration
	// Now is a clock injection point for tests. Defaults to time.Now.
	Now func() time.Time
	// OnError observes a purge error; nil is a no-op. The loop never aborts.
	OnError func(err error)
	// OnPurge observes the number of rows removed by a tick (only called when
	// deleted > 0); nil is a no-op.
	OnPurge func(deleted int64)
}

// HardDeleteGCCron periodically hard-deletes long-soft-deleted memory records.
type HardDeleteGCCron struct {
	purge     func(ctx context.Context, cutoff time.Time) (int64, error)
	retention time.Duration
	interval  time.Duration
	now       func() time.Time
	onError   func(err error)
	onPurge   func(deleted int64)

	stop chan struct{}
	done chan struct{}
}

// NewHardDeleteGCCron constructs the GC. It does not start the loop — call
// Run(ctx) (typically in a goroutine) once wired into the lifecycle.
func NewHardDeleteGCCron(cfg HardDeleteGCCronConfig) *HardDeleteGCCron {
	retention := cfg.Retention
	if retention <= 0 {
		retention = 30 * 24 * time.Hour
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = time.Hour
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	onError := cfg.OnError
	if onError == nil {
		onError = func(error) {}
	}
	onPurge := cfg.OnPurge
	if onPurge == nil {
		onPurge = func(int64) {}
	}
	return &HardDeleteGCCron{
		purge:     cfg.Purge,
		retention: retention,
		interval:  interval,
		now:       now,
		onError:   onError,
		onPurge:   onPurge,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
}

// Run drives the ticker loop. Returns when ctx is cancelled or Stop is called.
// Safe to call at most once per cron instance.
func (c *HardDeleteGCCron) Run(ctx context.Context) {
	defer close(c.done)
	if c.purge == nil {
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

// Stop signals the loop to exit and blocks until it does (bounded by ctx if the
// caller provides a deadline). Idempotent.
func (c *HardDeleteGCCron) Stop(ctx context.Context) {
	select {
	case <-c.stop:
	default:
		close(c.stop)
	}
	select {
	case <-c.done:
	case <-ctx.Done():
	}
}

// runOnce performs a single purge cycle: physically remove rows soft-deleted
// before now-Retention. A purge error is reported and the tick skipped; the
// next tick retries.
func (c *HardDeleteGCCron) runOnce(ctx context.Context) {
	cutoff := c.now().UTC().Add(-c.retention)
	deleted, err := c.purge(ctx, cutoff)
	if err != nil {
		c.onError(err)
		return
	}
	if deleted > 0 {
		c.onPurge(deleted)
	}
}
