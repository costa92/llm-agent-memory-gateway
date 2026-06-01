package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestSink builds a sink with conservative defaults for tests. Callers can
// override fields before starting Run.
func newTestSink(t *testing.T, insertFunc func(ctx context.Context, rows []TraceRow) error) *PostgresDecisionTraceSink {
	t.Helper()
	sink := NewPostgresDecisionTraceSink(PostgresDecisionTraceSinkConfig{
		InsertFunc:      insertFunc,
		BufferSize:      8,
		BatchSize:       3,
		FlushInterval:   25 * time.Millisecond,
		ShutdownTimeout: 1 * time.Second,
		RetryBackoff:    []time.Duration{1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Millisecond},
	})
	return sink
}

func TestPostgresDecisionTraceSink_RecordAndDrain(t *testing.T) {
	var mu sync.Mutex
	var seen []TraceRow
	insert := func(_ context.Context, rows []TraceRow) error {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, rows...)
		return nil
	}
	sink := newTestSink(t, insert)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sink.Run(ctx)

	for i := 0; i < 3; i++ {
		if err := sink.Record(context.Background(), TraceRow{Stage: "recalled", TenantID: "t"}); err != nil {
			t.Fatalf("Record err=%v", err)
		}
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	sink.Stop(stopCtx)

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 3 {
		t.Fatalf("inserted=%d, want 3", len(seen))
	}
	if snap := sink.DroppedSnapshot(); snap.BufferFull != 0 || snap.DBError != 0 || snap.Shutdown != 0 {
		t.Fatalf("unexpected drops: %+v", snap)
	}
}

func TestPostgresDecisionTraceSink_NonBlockingOnFullBuffer(t *testing.T) {
	// Block the insert until released. With BufferSize=8 + BatchSize=3, we will
	// flood beyond capacity to force overflow.
	release := make(chan struct{})
	var inFlight atomic.Int32
	insert := func(ctx context.Context, _ []TraceRow) error {
		inFlight.Add(1)
		defer inFlight.Add(-1)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	sink := newTestSink(t, insert)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sink.Run(ctx)

	// Push enough rows that the channel saturates. Each Record is non-blocking;
	// once the writer is parked inside insert (and the buffer of 8 is full), the
	// remaining sends must take the default branch and bump BufferFull.
	const attempts = 200
	for i := 0; i < attempts; i++ {
		_ = sink.Record(context.Background(), TraceRow{Stage: "recalled"})
	}

	if sink.DroppedSnapshot().BufferFull == 0 {
		t.Fatalf("expected BufferFull drops > 0, got %+v", sink.DroppedSnapshot())
	}

	close(release)
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	sink.Stop(stopCtx)
}

func TestPostgresDecisionTraceSink_BatchFlushBySize(t *testing.T) {
	var mu sync.Mutex
	var batchSizes []int
	insert := func(_ context.Context, rows []TraceRow) error {
		mu.Lock()
		batchSizes = append(batchSizes, len(rows))
		mu.Unlock()
		return nil
	}
	// Use a long flush interval so the only way batches form is by size.
	sink := NewPostgresDecisionTraceSink(PostgresDecisionTraceSinkConfig{
		InsertFunc:      insert,
		BufferSize:      32,
		BatchSize:       3,
		FlushInterval:   10 * time.Second,
		ShutdownTimeout: 1 * time.Second,
		RetryBackoff:    []time.Duration{1 * time.Millisecond},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sink.Run(ctx)

	for i := 0; i < 6; i++ {
		if err := sink.Record(context.Background(), TraceRow{Stage: "recalled"}); err != nil {
			t.Fatalf("Record err=%v", err)
		}
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	sink.Stop(stopCtx)

	mu.Lock()
	defer mu.Unlock()
	if len(batchSizes) < 2 {
		t.Fatalf("expected at least 2 batch flushes, got %v", batchSizes)
	}
	// At least one full-size batch should have shipped before shutdown drain.
	saw3 := false
	for _, n := range batchSizes {
		if n == 3 {
			saw3 = true
		}
	}
	if !saw3 {
		t.Fatalf("expected a size-3 batch, got %v", batchSizes)
	}
}

func TestPostgresDecisionTraceSink_BatchFlushByTick(t *testing.T) {
	var mu sync.Mutex
	var flushed int
	insert := func(_ context.Context, rows []TraceRow) error {
		mu.Lock()
		flushed += len(rows)
		mu.Unlock()
		return nil
	}
	sink := NewPostgresDecisionTraceSink(PostgresDecisionTraceSinkConfig{
		InsertFunc:      insert,
		BufferSize:      8,
		BatchSize:       100,
		FlushInterval:   10 * time.Millisecond,
		ShutdownTimeout: 1 * time.Second,
		RetryBackoff:    []time.Duration{1 * time.Millisecond},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sink.Run(ctx)

	// Send a single row; only the tick can flush it because BatchSize is 100.
	if err := sink.Record(context.Background(), TraceRow{Stage: "recalled"}); err != nil {
		t.Fatalf("Record err=%v", err)
	}
	// Wait long enough for at least one tick to fire and flush.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		f := flushed
		mu.Unlock()
		if f >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	sink.Stop(stopCtx)

	mu.Lock()
	defer mu.Unlock()
	if flushed < 1 {
		t.Fatalf("expected tick to flush single row, got flushed=%d", flushed)
	}
}

func TestPostgresDecisionTraceSink_RetriesThenDrops(t *testing.T) {
	var attempts atomic.Int32
	bad := errors.New("db unavailable")
	insert := func(context.Context, []TraceRow) error {
		attempts.Add(1)
		return bad
	}
	sink := NewPostgresDecisionTraceSink(PostgresDecisionTraceSinkConfig{
		InsertFunc:      insert,
		BufferSize:      8,
		BatchSize:       2,
		FlushInterval:   10 * time.Second,
		ShutdownTimeout: 1 * time.Second,
		RetryBackoff:    []time.Duration{1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Millisecond},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sink.Run(ctx)

	if err := sink.Record(context.Background(), TraceRow{Stage: "recalled"}); err != nil {
		t.Fatalf("Record err=%v", err)
	}
	if err := sink.Record(context.Background(), TraceRow{Stage: "recalled"}); err != nil {
		t.Fatalf("Record err=%v", err)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	sink.Stop(stopCtx)

	if got := attempts.Load(); got < 3 {
		t.Fatalf("expected at least 3 insert attempts (retries), got %d", got)
	}
	if snap := sink.DroppedSnapshot(); snap.DBError < 2 {
		t.Fatalf("expected DBError drops>=2, got %+v", snap)
	}
}

func TestPostgresDecisionTraceSink_ShutdownDrainBudget(t *testing.T) {
	// Insert blocks until ctx cancels. Shutdown must give up after the deadline
	// and count the unflushed rows as Shutdown drops.
	insert := func(ctx context.Context, _ []TraceRow) error {
		<-ctx.Done()
		return ctx.Err()
	}
	sink := NewPostgresDecisionTraceSink(PostgresDecisionTraceSinkConfig{
		InsertFunc:      insert,
		BufferSize:      16,
		BatchSize:       4,
		FlushInterval:   10 * time.Second,
		ShutdownTimeout: 20 * time.Millisecond,
		RetryBackoff:    []time.Duration{1 * time.Millisecond},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sink.Run(ctx)

	for i := 0; i < 5; i++ {
		_ = sink.Record(context.Background(), TraceRow{Stage: "recalled"})
	}

	// Give the writer a moment to pick up the first batch and park inside insert.
	time.Sleep(20 * time.Millisecond)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer stopCancel()
	sink.Stop(stopCtx)

	if snap := sink.DroppedSnapshot(); snap.Shutdown == 0 {
		t.Fatalf("expected Shutdown drops > 0, got %+v", snap)
	}
}

// Compile-time assertion: *PostgresDecisionTraceSink implements DecisionTraceSink.
var _ DecisionTraceSink = (*PostgresDecisionTraceSink)(nil)
