package service

import (
	"context"
	"time"
)

// IdleSessionScope identifies one session whose working memory has gone idle.
type IdleSessionScope struct {
	TenantID  string
	UserID    string
	ProjectID string
	SessionID string
}

// SessionReaperCronConfig wires the orphaned-session reaper (D6).
//
// ListIdle and Close are required. ListIdle returns the scopes of sessions
// whose newest working-record activity is older than the supplied cutoff —
// these are sessions that were used but never explicitly closed (the
// session-close endpoint is the only thing that reclaims working memory, and
// abandoned clients never call it). Close reclaims one such session, typically
// Service.ReapIdleSession (CloseSession with mode=expire_working).
//
// The reaper keys off the working RECORDS, not the session-state table,
// because an active session is not registered in session-state unless the
// client calls heartbeat/close — so the dominant leak (never-heartbeated,
// never-closed sessions) is invisible to a session-state scan.
type SessionReaperCronConfig struct {
	ListIdle func(ctx context.Context, cutoff time.Time) ([]IdleSessionScope, error)
	Close    func(ctx context.Context, scope IdleSessionScope) error
	// IdleTTL is how long a session's working memory may be idle before it is
	// reaped. Defaults to 30m (matches the session idle TTL).
	IdleTTL time.Duration
	// Interval is how often the reaper scans. Defaults to 5m.
	Interval time.Duration
	// Now is a clock injection point for tests. Defaults to time.Now.
	Now func() time.Time
	// OnError observes a tick's list error or a per-session close error. nil is
	// a no-op. The reaper never aborts the loop on error.
	OnError func(err error)
}

// SessionReaperCron periodically reclaims the working memory of idle, never
// explicitly closed sessions (D6). Sessions still being written or recalled
// stay alive because their working records' activity timestamp keeps
// advancing past the cutoff.
type SessionReaperCron struct {
	listIdle func(ctx context.Context, cutoff time.Time) ([]IdleSessionScope, error)
	close    func(ctx context.Context, scope IdleSessionScope) error
	idleTTL  time.Duration
	interval time.Duration
	now      func() time.Time
	onError  func(err error)

	stop chan struct{}
	done chan struct{}
}

// NewSessionReaperCron constructs the reaper. It does not start the loop —
// call Run(ctx) (typically in a goroutine) once wired into the lifecycle.
func NewSessionReaperCron(cfg SessionReaperCronConfig) *SessionReaperCron {
	idleTTL := cfg.IdleTTL
	if idleTTL <= 0 {
		idleTTL = 30 * time.Minute
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	onError := cfg.OnError
	if onError == nil {
		onError = func(error) {}
	}
	return &SessionReaperCron{
		listIdle: cfg.ListIdle,
		close:    cfg.Close,
		idleTTL:  idleTTL,
		interval: interval,
		now:      now,
		onError:  onError,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Run drives the ticker loop. Returns when ctx is cancelled or Stop is called.
// Safe to call at most once per cron instance.
func (c *SessionReaperCron) Run(ctx context.Context) {
	defer close(c.done)
	if c.listIdle == nil || c.close == nil {
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
func (c *SessionReaperCron) Stop(ctx context.Context) {
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

// runOnce performs a single scan+reclaim cycle. A list error is reported and
// the tick is skipped (the next tick retries). A per-session close error is
// reported and the reaper moves on to the next session, so one bad session
// does not stall reclamation of the rest. Idempotent: an already-closed or
// already-emptied session is a no-op downstream.
func (c *SessionReaperCron) runOnce(ctx context.Context) {
	cutoff := c.now().UTC().Add(-c.idleTTL)
	scopes, err := c.listIdle(ctx, cutoff)
	if err != nil {
		c.onError(err)
		return
	}
	for _, scope := range scopes {
		select {
		case <-ctx.Done():
			return
		case <-c.stop:
			return
		default:
		}
		if err := c.close(ctx, scope); err != nil {
			c.onError(err)
		}
	}
}
