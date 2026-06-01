package service

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestCrossTenant_TraceRowsIsolated asserts the sink does not leak tenant_a
// trace rows into a tenant_b query. It is a property test of the sink's
// downstream consumer separation: rows are tagged with TenantID and a query
// keyed on a different tenant must return none.
func TestCrossTenant_TraceRowsIsolated(t *testing.T) {
	var mu sync.Mutex
	var captured []TraceRow
	insert := func(_ context.Context, rows []TraceRow) error {
		mu.Lock()
		defer mu.Unlock()
		captured = append(captured, rows...)
		return nil
	}

	sink := NewPostgresDecisionTraceSink(PostgresDecisionTraceSinkConfig{
		InsertFunc:      insert,
		BufferSize:      16,
		BatchSize:       4,
		FlushInterval:   10 * time.Millisecond,
		ShutdownTimeout: 1 * time.Second,
		RetryBackoff:    []time.Duration{1 * time.Millisecond},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sink.Run(ctx)

	// Write 5 rows for tenant_a.
	for i := 0; i < 5; i++ {
		if err := sink.Record(ctx, TraceRow{TenantID: "tenant_a", Stage: "recalled", Reason: "ok"}); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	// And 0 rows for tenant_b.

	// Stop drains the queue.
	sink.Stop(ctx)

	mu.Lock()
	defer mu.Unlock()

	// Property: querying for tenant_b returns no rows.
	tenantBRows := filterTraceRowsByTenant(captured, "tenant_b")
	if len(tenantBRows) != 0 {
		t.Fatalf("tenant_b leak: %d rows when 0 expected", len(tenantBRows))
	}
	// Sanity: tenant_a's 5 rows are there.
	tenantARows := filterTraceRowsByTenant(captured, "tenant_a")
	if len(tenantARows) != 5 {
		t.Fatalf("tenant_a rows = %d, want 5", len(tenantARows))
	}
}

func filterTraceRowsByTenant(rows []TraceRow, tenantID string) []TraceRow {
	out := make([]TraceRow, 0, len(rows))
	for _, row := range rows {
		if row.TenantID == tenantID {
			out = append(out, row)
		}
	}
	return out
}

// fakeStorageMetricsSink records SetMemoryStorageBytes calls per bucket so the
// cron's per-bucket isolation can be asserted in tests without observability.
type fakeStorageMetricsSink struct {
	mu       sync.Mutex
	memory   map[string]uint64
	vector   map[string]uint64
	failures atomic.Uint64
}

func newFakeStorageMetricsSink() *fakeStorageMetricsSink {
	return &fakeStorageMetricsSink{
		memory: make(map[string]uint64),
		vector: make(map[string]uint64),
	}
}

func (f *fakeStorageMetricsSink) SetMemoryStorageBytes(bucket string, b uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.memory[bucket] = b
}
func (f *fakeStorageMetricsSink) SetVectorStorageBytes(bucket string, b uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vector[bucket] = b
}
func (f *fakeStorageMetricsSink) AddStorageCronFailure() { f.failures.Add(1) }

// TestCrossTenant_StorageMetricsIsolated asserts the storage cron aggregates
// per-tenant rows into the right bucket and does not bleed bytes from one
// bucket into another. We pick two tenant IDs whose buckets are known to differ.
func TestCrossTenant_StorageMetricsIsolated(t *testing.T) {
	// Pick two tenants whose buckets differ. Loop a small space so the test
	// remains deterministic without coupling to the exact hash.
	tenantA, tenantB := pickTwoTenantsInDifferentBuckets(t)
	bucketA := tenantBucket(tenantA)
	bucketB := tenantBucket(tenantB)

	sink := newFakeStorageMetricsSink()
	cron := NewStorageMetricsCron(StorageMetricsCronConfig{
		Query: func(_ context.Context) ([]StorageRow, error) {
			return []StorageRow{
				{TenantID: tenantA, MemoryBytes: 100, VectorBytes: 0},
				{TenantID: tenantB, MemoryBytes: 200, VectorBytes: 0},
			}, nil
		},
		Metrics:  sink,
		Interval: time.Hour, // we drive runOnce directly
	})
	cron.runOnce(context.Background())

	sink.mu.Lock()
	defer sink.mu.Unlock()

	if sink.memory[bucketA] != 100 {
		t.Fatalf("bucketA memory = %d, want 100", sink.memory[bucketA])
	}
	if sink.memory[bucketB] != 200 {
		t.Fatalf("bucketB memory = %d, want 200", sink.memory[bucketB])
	}
	// Cross-bleed: tenantA's bytes should not show up in bucketB.
	if sink.memory[bucketB] == 100 || sink.memory[bucketA] == 200 {
		t.Fatalf("cross-tenant bleed detected: memory=%v", sink.memory)
	}
}

// pickTwoTenantsInDifferentBuckets returns two tenant_id strings whose
// tenant_bucket values differ. With modulus=32 the search converges in a few
// iterations.
func pickTwoTenantsInDifferentBuckets(t *testing.T) (string, string) {
	t.Helper()
	base := "tenant_"
	first := base + "0"
	firstBucket := tenantBucket(first)
	for i := 1; i < 64; i++ {
		candidate := base + strconv.Itoa(i)
		if tenantBucket(candidate) != firstBucket {
			return first, candidate
		}
	}
	t.Fatal("could not find two tenants in different buckets within 64 tries")
	return "", ""
}

// TestCrossTenant_CounterBucketingStable asserts the bucketing function is a
// one-way aggregation: two distinct tenant IDs that land in the same bucket
// are indistinguishable at the metric layer (so forging a tenant_id cannot
// "read" the legitimate tenant's bucket count back), AND increments from one
// "forged" tenant into a shared bucket are not corrupted by being aliased
// onto the same bucket — they simply sum. This documents the privacy property:
// the bucket counter is a strict sum, not a per-tenant value.
func TestCrossTenant_CounterBucketingStable(t *testing.T) {
	// Find two tenants that share a bucket (a forced collision).
	tenantA, tenantB := pickTwoTenantsInSameBucket(t)
	bucket := tenantBucket(tenantA)
	if tenantBucket(tenantB) != bucket {
		t.Fatalf("test precondition: %q and %q must share a bucket", tenantA, tenantB)
	}

	sink := newFakeStorageMetricsSink()
	cron := NewStorageMetricsCron(StorageMetricsCronConfig{
		Query: func(_ context.Context) ([]StorageRow, error) {
			return []StorageRow{
				{TenantID: tenantA, MemoryBytes: 10},
				{TenantID: tenantB, MemoryBytes: 30},
			}, nil
		},
		Metrics:  sink,
		Interval: time.Hour,
	})
	cron.runOnce(context.Background())

	sink.mu.Lock()
	defer sink.mu.Unlock()

	got := sink.memory[bucket]
	if got != 40 {
		t.Fatalf("shared-bucket memory = %d, want 40 (10+30 from both tenants)", got)
	}
	// Property: an observer reading the bucket cannot recover which tenant
	// contributed which share — both legitimate. This is the documented
	// privacy guarantee of bucket aggregation.
}

// pickTwoTenantsInSameBucket returns two tenant_id strings that hash into the
// same bucket. With modulus 32 and the 32-bit FNV hash, a collision is
// guaranteed within ~6 tries by birthday paradox; we loop generously.
func pickTwoTenantsInSameBucket(t *testing.T) (string, string) {
	t.Helper()
	seen := make(map[string]string)
	for i := 0; i < 2048; i++ {
		id := "tenant_collide_" + strconv.Itoa(i)
		bucket := tenantBucket(id)
		if existing, ok := seen[bucket]; ok && existing != id {
			return existing, id
		}
		seen[bucket] = id
	}
	t.Fatal("could not force a bucket collision within 2048 tries")
	return "", ""
}
