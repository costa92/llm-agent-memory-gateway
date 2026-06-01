package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeStorageMetrics records SetMemoryStorageBytes / SetVectorStorageBytes /
// AddStorageCronFailure invocations so the cron can be tested without coupling
// to the real *observability.Metrics shape (which would create an import
// cycle: observability already imports service). The real
// *observability.Metrics satisfies the StorageMetricsSink interface and is
// wired in cmd/memory-gateway/main.go at composition time.
type fakeStorageMetrics struct {
	mu          sync.Mutex
	memoryBytes map[string]uint64
	vectorBytes map[string]uint64
	failures    uint64
}

func newFakeStorageMetrics() *fakeStorageMetrics {
	return &fakeStorageMetrics{
		memoryBytes: make(map[string]uint64),
		vectorBytes: make(map[string]uint64),
	}
}

func (f *fakeStorageMetrics) SetMemoryStorageBytes(bucket string, b uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.memoryBytes[bucket] = b
}

func (f *fakeStorageMetrics) SetVectorStorageBytes(bucket string, b uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vectorBytes[bucket] = b
}

func (f *fakeStorageMetrics) AddStorageCronFailure() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures++
}

func (f *fakeStorageMetrics) memoryBytesOf(bucket string) uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.memoryBytes[bucket]
}

func (f *fakeStorageMetrics) vectorBytesOf(bucket string) uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.vectorBytes[bucket]
}

func (f *fakeStorageMetrics) failuresTotal() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failures
}

func (f *fakeStorageMetrics) memoryBytesLen() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.memoryBytes)
}

func TestStorageMetricsCron_TickInvokesQueryAndUpdatesMetrics(t *testing.T) {
	m := newFakeStorageMetrics()

	var calls atomic.Int32
	query := func(_ context.Context) ([]StorageRow, error) {
		calls.Add(1)
		return []StorageRow{
			{TenantID: "tenant-a", MemoryBytes: 100, VectorBytes: 50},
			{TenantID: "tenant-b", MemoryBytes: 200, VectorBytes: 80},
		}, nil
	}

	cron := NewStorageMetricsCron(StorageMetricsCronConfig{
		Query:    query,
		Metrics:  m,
		Interval: 5 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		cron.Run(ctx)
		close(done)
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if calls.Load() >= 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if calls.Load() < 1 {
		t.Fatalf("query never invoked")
	}

	// Give the cron a moment to populate metrics after the tick.
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if m.memoryBytesLen() >= 2 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	cancel()
	<-done

	bucketA := tenantBucket("tenant-a")
	bucketB := tenantBucket("tenant-b")
	if got := m.memoryBytesOf(bucketA); got != 100 {
		t.Fatalf("memory bytes bucket %s = %d, want 100", bucketA, got)
	}
	if got := m.vectorBytesOf(bucketA); got != 50 {
		t.Fatalf("vector bytes bucket %s = %d, want 50", bucketA, got)
	}
	if got := m.memoryBytesOf(bucketB); got != 200 {
		t.Fatalf("memory bytes bucket %s = %d, want 200", bucketB, got)
	}
	if got := m.vectorBytesOf(bucketB); got != 80 {
		t.Fatalf("vector bytes bucket %s = %d, want 80", bucketB, got)
	}
}

func TestStorageMetricsCron_AggregatesRowsInSameBucket(t *testing.T) {
	m := newFakeStorageMetrics()

	// One tenant_id, two rows: cron must sum within the bucket when collapsing
	// tenant rows. (Postgres GROUP BY tenant_id is the natural shape but
	// belt-and-suspenders aggregation in Go protects against duplicate-row
	// pathologies.)
	query := func(_ context.Context) ([]StorageRow, error) {
		return []StorageRow{
			{TenantID: "tenant-x", MemoryBytes: 100, VectorBytes: 30},
			{TenantID: "tenant-x", MemoryBytes: 200, VectorBytes: 70},
		}, nil
	}

	cron := NewStorageMetricsCron(StorageMetricsCronConfig{
		Query:    query,
		Metrics:  m,
		Interval: 5 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		cron.Run(ctx)
		close(done)
	}()

	bucket := tenantBucket("tenant-x")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if m.memoryBytesOf(bucket) == 300 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	<-done

	if got := m.memoryBytesOf(bucket); got != 300 {
		t.Fatalf("memory bytes bucket %s = %d, want 300 (summed within bucket)", bucket, got)
	}
	if got := m.vectorBytesOf(bucket); got != 100 {
		t.Fatalf("vector bytes bucket %s = %d, want 100", bucket, got)
	}
}

func TestStorageMetricsCron_QueryFailureIncrementsFailureCounter(t *testing.T) {
	m := newFakeStorageMetrics()

	var queryCalls atomic.Int32
	var mu sync.Mutex
	failing := true
	query := func(_ context.Context) ([]StorageRow, error) {
		queryCalls.Add(1)
		mu.Lock()
		shouldFail := failing
		mu.Unlock()
		if shouldFail {
			return nil, errors.New("boom")
		}
		return []StorageRow{{TenantID: "tenant-recovered", MemoryBytes: 42, VectorBytes: 7}}, nil
	}

	cron := NewStorageMetricsCron(StorageMetricsCronConfig{
		Query:    query,
		Metrics:  m,
		Interval: 5 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		cron.Run(ctx)
		close(done)
	}()

	// Wait for first failure to land.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if m.failuresTotal() >= 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	if m.failuresTotal() < 1 {
		t.Fatalf("storage_cron_failures_total not incremented (= %d)", m.failuresTotal())
	}
	// Storage maps must remain empty after a failed tick.
	if m.memoryBytesLen() != 0 {
		t.Fatalf("memory bytes map should be empty after failure")
	}

	// Recover; next tick should populate metrics.
	mu.Lock()
	failing = false
	mu.Unlock()

	bucket := tenantBucket("tenant-recovered")
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if m.memoryBytesOf(bucket) == 42 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	<-done

	if got := m.memoryBytesOf(bucket); got != 42 {
		t.Fatalf("post-recovery memory bytes bucket %s = %d, want 42", bucket, got)
	}
}

func TestStorageMetricsCron_StopCancelsPromptly(t *testing.T) {
	m := newFakeStorageMetrics()
	query := func(_ context.Context) ([]StorageRow, error) { return nil, nil }
	cron := NewStorageMetricsCron(StorageMetricsCronConfig{
		Query:    query,
		Metrics:  m,
		Interval: time.Hour, // intentionally long; Stop must not wait for the ticker.
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		cron.Run(ctx)
		close(done)
	}()

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer stopCancel()

	start := time.Now()
	cron.Stop(stopCtx)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("cron did not exit after Stop")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Stop took %v, expected prompt shutdown", elapsed)
	}
}
