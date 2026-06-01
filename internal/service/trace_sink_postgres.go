package service

import (
	"context"
	"sync/atomic"
	"time"
)

// defaultTraceSinkRetryBackoff matches the failure-policy section of the M7 plan:
// three attempts at 50ms / 200ms / 800ms before the row batch is counted as a
// DB-error drop.
var defaultTraceSinkRetryBackoff = []time.Duration{
	50 * time.Millisecond,
	200 * time.Millisecond,
	800 * time.Millisecond,
}

// PostgresDecisionTraceSinkConfig configures a PostgresDecisionTraceSink. The
// sink is decoupled from any concrete database client: callers wire their own
// InsertFunc (typically a pgx batch insert closure) at construction.
type PostgresDecisionTraceSinkConfig struct {
	// InsertFunc is called by the writer goroutine with up to BatchSize rows
	// per call. It must be safe to call from a single goroutine.
	InsertFunc func(ctx context.Context, rows []TraceRow) error
	// BufferSize bounds the in-memory queue. When full, Record drops rows
	// non-blockingly and bumps the BufferFull counter.
	BufferSize int
	// BatchSize is the maximum rows per InsertFunc call.
	BatchSize int
	// FlushInterval bounds how long a partial batch can sit before being
	// flushed even if BatchSize isn't reached.
	FlushInterval time.Duration
	// ShutdownTimeout bounds how long Stop will wait for the writer goroutine
	// to drain queued rows.
	ShutdownTimeout time.Duration
	// RetryBackoff sets the per-attempt sleep durations. len(RetryBackoff)
	// is the total number of attempts (default: 3 attempts at 50/200/800ms).
	RetryBackoff []time.Duration
}

// TraceDroppedSnapshot is a read-only view of the sink's drop counters. Each
// reason is mutually exclusive: a row is counted in exactly one bucket.
type TraceDroppedSnapshot struct {
	BufferFull uint64
	DBError    uint64
	Shutdown   uint64
}

// PostgresDecisionTraceSink is a bounded, best-effort async sink. The
// request-path Record method is non-blocking; durability is the writer
// goroutine's responsibility.
type PostgresDecisionTraceSink struct {
	insertFunc      func(ctx context.Context, rows []TraceRow) error
	ch              chan TraceRow
	batchSize       int
	flushInterval   time.Duration
	shutdownTimeout time.Duration
	retryBackoff    []time.Duration

	droppedBufferFull atomic.Uint64
	droppedDBError    atomic.Uint64
	droppedShutdown   atomic.Uint64

	stop chan struct{}
	done chan struct{}
}

// NewPostgresDecisionTraceSink constructs a sink. It does not start the writer
// goroutine — call Run(ctx) (typically in a separate goroutine) once construction
// is wired into the lifecycle.
func NewPostgresDecisionTraceSink(cfg PostgresDecisionTraceSinkConfig) *PostgresDecisionTraceSink {
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 1024
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 250 * time.Millisecond
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 5 * time.Second
	}
	if len(cfg.RetryBackoff) == 0 {
		cfg.RetryBackoff = defaultTraceSinkRetryBackoff
	}
	return &PostgresDecisionTraceSink{
		insertFunc:      cfg.InsertFunc,
		ch:              make(chan TraceRow, cfg.BufferSize),
		batchSize:       cfg.BatchSize,
		flushInterval:   cfg.FlushInterval,
		shutdownTimeout: cfg.ShutdownTimeout,
		retryBackoff:    cfg.RetryBackoff,
		stop:            make(chan struct{}),
		done:            make(chan struct{}),
	}
}

// Record enqueues a row for asynchronous insert. It never blocks: if the
// internal buffer is full, the row is dropped and the BufferFull counter is
// incremented. The returned error is always nil — drops are surfaced through
// DroppedSnapshot, not the request path.
func (s *PostgresDecisionTraceSink) Record(_ context.Context, row TraceRow) error {
	select {
	case s.ch <- row:
	default:
		s.droppedBufferFull.Add(1)
	}
	return nil
}

// Run is the writer goroutine. It returns when ctx is cancelled or Stop is
// called. Run must not be called more than once per sink.
func (s *PostgresDecisionTraceSink) Run(ctx context.Context) {
	defer close(s.done)

	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()

	batch := make([]TraceRow, 0, s.batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		// Steady-state flushes use the writer's ctx — they get the full
		// retry budget. Shutdown-time flushes go through drainOnShutdown
		// with a deadline-bound context instead.
		if !s.tryInsert(ctx, batch) {
			s.droppedDBError.Add(uint64(len(batch)))
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			s.drainOnShutdown(batch)
			return
		case <-s.stop:
			s.drainOnShutdown(batch)
			return
		case row := <-s.ch:
			batch = append(batch, row)
			if len(batch) >= s.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// Stop signals the writer to drain and waits up to ShutdownTimeout. Any rows
// that cannot be flushed within the budget are counted as Shutdown drops. If
// the writer goroutine is wedged inside an insertFunc call when the budget
// expires, the rows still queued in the channel are force-counted as
// Shutdown drops so observability remains accurate.
func (s *PostgresDecisionTraceSink) Stop(ctx context.Context) {
	select {
	case <-s.stop:
		// already stopped
	default:
		close(s.stop)
	}
	deadline := time.NewTimer(s.shutdownTimeout)
	defer deadline.Stop()
	select {
	case <-s.done:
		return
	case <-deadline.C:
	case <-ctx.Done():
	}
	// Writer didn't finish within the budget. Force-count anything still
	// queued in the channel as Shutdown drops.
	s.countRemainingAsShutdownDrops()
}

// DroppedSnapshot returns the current count of dropped rows per reason. The
// snapshot is best-effort consistent — values are loaded independently.
func (s *PostgresDecisionTraceSink) DroppedSnapshot() TraceDroppedSnapshot {
	return TraceDroppedSnapshot{
		BufferFull: s.droppedBufferFull.Load(),
		DBError:    s.droppedDBError.Load(),
		Shutdown:   s.droppedShutdown.Load(),
	}
}

// tryInsert runs the insert with bounded retries. Returns true on success.
// The retry loop honors ctx cancellation in the backoff sleep — callers
// should hand in a ctx that cancels on shutdown/deadline.
func (s *PostgresDecisionTraceSink) tryInsert(ctx context.Context, batch []TraceRow) bool {
	if s.insertFunc == nil {
		return true
	}
	// Defensive copy so a retry loop never observes an aliased slice that the
	// caller may reuse.
	rows := make([]TraceRow, len(batch))
	copy(rows, batch)

	for attempt, backoff := range s.retryBackoff {
		err := s.insertFunc(ctx, rows)
		if err == nil {
			return true
		}
		// On the final attempt, do not sleep — the caller will count drops.
		if attempt == len(s.retryBackoff)-1 {
			break
		}
		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return false
		}
	}
	return false
}

// drainOnShutdown flushes both the in-flight batch and anything still queued in
// the channel, respecting the shutdown budget. Rows that cannot be flushed in
// time are counted as Shutdown drops. The shared retry loop is reused so a
// transient DB error during drain still gets the configured retry budget,
// bounded by the deadline-aware drain context.
func (s *PostgresDecisionTraceSink) drainOnShutdown(pending []TraceRow) {
	deadline := time.Now().Add(s.shutdownTimeout)
	drainCtx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	batch := pending
	flushOne := func() {
		if len(batch) == 0 {
			return
		}
		if !s.tryInsert(drainCtx, batch) {
			// Distinguish between a real DB failure (insert returned error
			// each attempt) and a deadline-exceeded shutdown (drainCtx
			// expired). The former is a DBError drop; the latter is
			// Shutdown — keeps the counters faithful to the spec.
			if drainCtx.Err() != nil {
				s.droppedShutdown.Add(uint64(len(batch)))
			} else {
				s.droppedDBError.Add(uint64(len(batch)))
			}
		}
		batch = batch[:0]
	}

	// Pull every remaining row from the channel, respecting the deadline.
	for {
		if time.Now().After(deadline) {
			s.droppedShutdown.Add(uint64(len(batch)))
			batch = batch[:0]
			s.countRemainingAsShutdownDrops()
			return
		}
		select {
		case row, ok := <-s.ch:
			if !ok {
				flushOne()
				return
			}
			batch = append(batch, row)
			if len(batch) >= s.batchSize {
				flushOne()
			}
		default:
			flushOne()
			s.countRemainingAsShutdownDrops()
			return
		}
	}
}

// countRemainingAsShutdownDrops drains any rows still queued without
// attempting an insert and bumps the Shutdown counter for each.
func (s *PostgresDecisionTraceSink) countRemainingAsShutdownDrops() {
	for {
		select {
		case <-s.ch:
			s.droppedShutdown.Add(1)
		default:
			return
		}
	}
}
