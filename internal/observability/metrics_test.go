package observability

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/costa92/llm-agent-memory-gateway/internal/service"
)

func TestMetrics_ExposeCounters(t *testing.T) {
	m := NewMetrics()
	m.AddRecallOrigin()
	m.AddRecallL1Hit()
	m.AddRecallStaleServed()
	m.AddOutboxProjected()
	m.AddOutboxStale()
	m.AddOutboxFailed()
	m.AddOutboxIgnored()

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(recorder, req)

	body := recorder.Body.String()
	for _, want := range []string{
		"recall_origin_total 1",
		"recall_l1_hit_total 1",
		"recall_stale_served_total 1",
		"recall_cache_fill_total 0",
		"recall_invalidation_total 0",
		"outbox_projection_projected_total 1",
		"outbox_projection_stale_total 1",
		"outbox_projection_failed_total 1",
		"outbox_projection_ignored_total 1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics output missing %q\n%s", want, body)
		}
	}
}

func TestMetrics_OutboxObserverCountsStatuses(t *testing.T) {
	m := NewMetrics()
	observer := m.OutboxObserver()
	observer.ObserveProjection(context.Background(), service.OutboxProjectionObservation{Status: "projected"})
	observer.ObserveProjection(context.Background(), service.OutboxProjectionObservation{Status: "stale"})
	observer.ObserveProjection(context.Background(), service.OutboxProjectionObservation{Status: "failed"})
	observer.ObserveProjection(context.Background(), service.OutboxProjectionObservation{Status: "ignored"})

	snap := m.Snapshot()
	if snap.OutboxProjectionProjectedTotal != 1 || snap.OutboxProjectionStaleTotal != 1 || snap.OutboxProjectionFailedTotal != 1 || snap.OutboxProjectionIgnoredTotal != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestMetrics_RecallObserverCountsCacheLevels(t *testing.T) {
	m := NewMetrics()
	observer := m.RecallObserver()
	observer.ObserveRecall(context.Background(), service.RecallObservation{CacheLevel: "l1_hit", StaleServed: false})
	observer.ObserveRecall(context.Background(), service.RecallObservation{CacheLevel: "l1_hit", StaleServed: true})
	observer.ObserveRecall(context.Background(), service.RecallObservation{CacheLevel: "l2_hit", StaleServed: false})
	observer.ObserveRecall(context.Background(), service.RecallObservation{CacheLevel: "origin", StaleServed: false})

	snap := m.Snapshot()
	if snap.RecallL1HitTotal != 2 || snap.RecallL2HitTotal != 1 || snap.RecallOriginTotal != 1 || snap.RecallStaleServedTotal != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestMetrics_RecallCacheObserverCountsLifecycleEvents(t *testing.T) {
	m := NewMetrics()
	observer := m.RecallCacheObserver()
	observer.ObserveRecallCache(context.Background(), service.RecallCacheObservation{Action: "fill"})
	observer.ObserveRecallCache(context.Background(), service.RecallCacheObservation{Action: "invalidate"})

	snap := m.Snapshot()
	if snap.RecallCacheFillTotal != 1 || snap.RecallInvalidationTotal != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}
}

// ---- M7 validation counters ----

func TestMetrics_AddEmbeddingRequest_AppearsInSnapshot(t *testing.T) {
	m := NewMetrics()
	m.AddEmbeddingRequest("00")
	m.AddEmbeddingRequest("00")
	m.AddEmbeddingRequest("17")
	snap := m.Snapshot()
	if got := snap.EmbeddingRequestTotal["00"]; got != 2 {
		t.Fatalf("bucket 00 = %d, want 2", got)
	}
	if got := snap.EmbeddingRequestTotal["17"]; got != 1 {
		t.Fatalf("bucket 17 = %d, want 1", got)
	}
}

func TestMetrics_HandlerExposes_EmbeddingRequest(t *testing.T) {
	m := NewMetrics()
	m.AddEmbeddingRequest("00")
	m.AddEmbeddingRequest("00")
	body := handlerBody(t, m)
	if !strings.Contains(body, `embedding_request_total{tenant_bucket="00"} 2`) {
		t.Fatalf("missing embedding_request_total line\n%s", body)
	}
}

func TestMetrics_AddEmbeddingApplied_AppearsInSnapshot(t *testing.T) {
	m := NewMetrics()
	m.AddEmbeddingApplied("05")
	snap := m.Snapshot()
	if snap.EmbeddingAppliedTotal["05"] != 1 {
		t.Fatalf("snap = %+v", snap.EmbeddingAppliedTotal)
	}
}

func TestMetrics_HandlerExposes_EmbeddingApplied(t *testing.T) {
	m := NewMetrics()
	m.AddEmbeddingApplied("05")
	body := handlerBody(t, m)
	if !strings.Contains(body, `embedding_applied_total{tenant_bucket="05"} 1`) {
		t.Fatalf("missing embedding_applied_total line\n%s", body)
	}
}

func TestMetrics_AddEmbeddingTokens_AppearsInSnapshot(t *testing.T) {
	m := NewMetrics()
	m.AddEmbeddingTokens("12", 100)
	m.AddEmbeddingTokens("12", 50)
	snap := m.Snapshot()
	if got := snap.EmbeddingTokensTotal["12"]; got != 150 {
		t.Fatalf("bucket 12 = %d, want 150", got)
	}
}

func TestMetrics_HandlerExposes_EmbeddingTokens(t *testing.T) {
	m := NewMetrics()
	m.AddEmbeddingTokens("12", 150)
	body := handlerBody(t, m)
	if !strings.Contains(body, `embedding_tokens_total{tenant_bucket="12"} 150`) {
		t.Fatalf("missing embedding_tokens_total line\n%s", body)
	}
}

func TestMetrics_AddEmbeddingCost_AppearsInSnapshot(t *testing.T) {
	m := NewMetrics()
	m.AddEmbeddingCost("03", 2500)
	snap := m.Snapshot()
	if got := snap.EmbeddingCostTotal["03"]; got != 2500 {
		t.Fatalf("bucket 03 = %d, want 2500", got)
	}
}

func TestMetrics_HandlerExposes_EmbeddingCost(t *testing.T) {
	m := NewMetrics()
	m.AddEmbeddingCost("03", 2500)
	body := handlerBody(t, m)
	if !strings.Contains(body, `embedding_cost_total{tenant_bucket="03"} 2500`) {
		t.Fatalf("missing embedding_cost_total line\n%s", body)
	}
}

func TestMetrics_SetMemoryStorageBytes_LastWriteWins(t *testing.T) {
	m := NewMetrics()
	m.SetMemoryStorageBytes("07", 1000)
	m.SetMemoryStorageBytes("07", 2000)
	snap := m.Snapshot()
	if got := snap.MemoryStorageBytesTotal["07"]; got != 2000 {
		t.Fatalf("bucket 07 = %d, want 2000 (last write wins)", got)
	}
}

func TestMetrics_HandlerExposes_MemoryStorageBytes(t *testing.T) {
	m := NewMetrics()
	m.SetMemoryStorageBytes("07", 2000)
	body := handlerBody(t, m)
	if !strings.Contains(body, `memory_storage_bytes_total{tenant_bucket="07"} 2000`) {
		t.Fatalf("missing memory_storage_bytes_total line\n%s", body)
	}
}

func TestMetrics_SetVectorStorageBytes_LastWriteWins(t *testing.T) {
	m := NewMetrics()
	m.SetVectorStorageBytes("09", 500)
	m.SetVectorStorageBytes("09", 750)
	snap := m.Snapshot()
	if got := snap.VectorStorageBytesTotal["09"]; got != 750 {
		t.Fatalf("bucket 09 = %d, want 750", got)
	}
}

func TestMetrics_HandlerExposes_VectorStorageBytes(t *testing.T) {
	m := NewMetrics()
	m.SetVectorStorageBytes("09", 750)
	body := handlerBody(t, m)
	if !strings.Contains(body, `vector_storage_bytes_total{tenant_bucket="09"} 750`) {
		t.Fatalf("missing vector_storage_bytes_total line\n%s", body)
	}
}

func TestMetrics_AddEpisodicDisabled_AppearsInSnapshot(t *testing.T) {
	m := NewMetrics()
	m.AddEpisodicDisabled("11")
	m.AddEpisodicDisabled("11")
	snap := m.Snapshot()
	if got := snap.EpisodicDisabledTotal["11"]; got != 2 {
		t.Fatalf("bucket 11 = %d, want 2", got)
	}
}

func TestMetrics_HandlerExposes_EpisodicDisabled(t *testing.T) {
	m := NewMetrics()
	m.AddEpisodicDisabled("11")
	body := handlerBody(t, m)
	if !strings.Contains(body, `episodic_disabled_total{tenant_bucket="11"} 1`) {
		t.Fatalf("missing episodic_disabled_total line\n%s", body)
	}
}

func TestMetrics_AddEpisodicDeleted_AppearsInSnapshot(t *testing.T) {
	m := NewMetrics()
	m.AddEpisodicDeleted("14")
	snap := m.Snapshot()
	if snap.EpisodicDeletedTotal["14"] != 1 {
		t.Fatalf("snap = %+v", snap.EpisodicDeletedTotal)
	}
}

func TestMetrics_HandlerExposes_EpisodicDeleted(t *testing.T) {
	m := NewMetrics()
	m.AddEpisodicDeleted("14")
	body := handlerBody(t, m)
	if !strings.Contains(body, `episodic_deleted_total{tenant_bucket="14"} 1`) {
		t.Fatalf("missing episodic_deleted_total line\n%s", body)
	}
}

func TestMetrics_AddRecallReturned_AppearsInSnapshot(t *testing.T) {
	m := NewMetrics()
	m.AddRecallReturned("02", 7)
	m.AddRecallReturned("02", 3)
	snap := m.Snapshot()
	if got := snap.RecallReturnedTotal["02"]; got != 10 {
		t.Fatalf("bucket 02 = %d, want 10", got)
	}
}

func TestMetrics_HandlerExposes_RecallReturned(t *testing.T) {
	m := NewMetrics()
	m.AddRecallReturned("02", 10)
	body := handlerBody(t, m)
	if !strings.Contains(body, `recall_returned_total{tenant_bucket="02"} 10`) {
		t.Fatalf("missing recall_returned_total line\n%s", body)
	}
}

func TestMetrics_AddRecallSelected_AppearsInSnapshot(t *testing.T) {
	m := NewMetrics()
	m.AddRecallSelected("22", 4)
	snap := m.Snapshot()
	if snap.RecallSelectedTotal["22"] != 4 {
		t.Fatalf("snap = %+v", snap.RecallSelectedTotal)
	}
}

func TestMetrics_HandlerExposes_RecallSelected(t *testing.T) {
	m := NewMetrics()
	m.AddRecallSelected("22", 4)
	body := handlerBody(t, m)
	if !strings.Contains(body, `recall_selected_total{tenant_bucket="22"} 4`) {
		t.Fatalf("missing recall_selected_total line\n%s", body)
	}
}

// ---- recall returned/selected counters (Task 9) ----

func TestRecallObserver_ReturnedIncrementsRecallReturned(t *testing.T) {
	m := NewMetrics()
	observer := m.RecallObserver()
	observer.ObserveRecall(context.Background(), service.RecallObservation{
		CacheLevel: "origin",
		TenantID:   "tenant-a",
		Returned:   7,
		Selected:   5,
	})

	bucket := service.TenantBucket("tenant-a")
	snap := m.Snapshot()
	if got := snap.RecallReturnedTotal[bucket]; got != 7 {
		t.Fatalf("recall_returned[%s] = %d, want 7", bucket, got)
	}
}

func TestRecallObserver_SelectedIncrementsRecallSelected(t *testing.T) {
	m := NewMetrics()
	observer := m.RecallObserver()
	observer.ObserveRecall(context.Background(), service.RecallObservation{
		CacheLevel: "origin",
		TenantID:   "tenant-b",
		Returned:   7,
		Selected:   5,
	})

	bucket := service.TenantBucket("tenant-b")
	snap := m.Snapshot()
	if got := snap.RecallSelectedTotal[bucket]; got != 5 {
		t.Fatalf("recall_selected[%s] = %d, want 5", bucket, got)
	}
}

func TestRecallObserver_ZeroHitsIncrementsNeitherCounter(t *testing.T) {
	m := NewMetrics()
	observer := m.RecallObserver()
	observer.ObserveRecall(context.Background(), service.RecallObservation{
		CacheLevel: "origin",
		TenantID:   "tenant-c",
		Returned:   0,
		Selected:   0,
	})

	snap := m.Snapshot()
	if len(snap.RecallReturnedTotal) != 0 {
		t.Fatalf("recall_returned should be empty on zero-hit recall: %+v", snap.RecallReturnedTotal)
	}
	if len(snap.RecallSelectedTotal) != 0 {
		t.Fatalf("recall_selected should be empty on zero-hit recall: %+v", snap.RecallSelectedTotal)
	}
}

func TestRecallObserver_BucketsByTenantID(t *testing.T) {
	m := NewMetrics()
	observer := m.RecallObserver()
	observer.ObserveRecall(context.Background(), service.RecallObservation{
		CacheLevel: "origin",
		TenantID:   "tenant-d",
		Returned:   3,
		Selected:   2,
	})

	bucket := service.TenantBucket("tenant-d")
	snap := m.Snapshot()
	if got := snap.RecallReturnedTotal[bucket]; got != 3 {
		t.Fatalf("recall_returned[%s] = %d, want 3", bucket, got)
	}
	if got := snap.RecallSelectedTotal[bucket]; got != 2 {
		t.Fatalf("recall_selected[%s] = %d, want 2", bucket, got)
	}
	// Confirm the raw tenant_id is not used as a label.
	if _, present := snap.RecallReturnedTotal["tenant-d"]; present {
		t.Fatalf("raw tenant_id leaked into recall_returned labels")
	}
}

// ---- outbox lifecycle counters (Task 8) ----

func TestOutboxObserver_DisabledIncrementsEpisodicDisabled(t *testing.T) {
	m := NewMetrics()
	observer := m.OutboxObserver()
	observer.ObserveProjection(context.Background(), service.OutboxProjectionObservation{
		Status:    "projected",
		EventType: "memory_disabled",
		TenantID:  "tenant-a",
	})

	wantBucket := service.TenantBucket("tenant-a")
	snap := m.Snapshot()
	if got := snap.EpisodicDisabledTotal[wantBucket]; got != 1 {
		t.Fatalf("episodic_disabled[%s] = %d, want 1", wantBucket, got)
	}
	if len(snap.EpisodicDeletedTotal) != 0 {
		t.Fatalf("episodic_deleted unexpectedly populated: %+v", snap.EpisodicDeletedTotal)
	}
}

func TestOutboxObserver_DeletedIncrementsEpisodicDeleted(t *testing.T) {
	m := NewMetrics()
	observer := m.OutboxObserver()
	observer.ObserveProjection(context.Background(), service.OutboxProjectionObservation{
		Status:    "projected",
		EventType: "memory_deleted",
		TenantID:  "tenant-b",
	})

	wantBucket := service.TenantBucket("tenant-b")
	snap := m.Snapshot()
	if got := snap.EpisodicDeletedTotal[wantBucket]; got != 1 {
		t.Fatalf("episodic_deleted[%s] = %d, want 1", wantBucket, got)
	}
	if len(snap.EpisodicDisabledTotal) != 0 {
		t.Fatalf("episodic_disabled unexpectedly populated: %+v", snap.EpisodicDisabledTotal)
	}
}

func TestOutboxObserver_OtherEventTypesDoNotTouchLifecycleCounters(t *testing.T) {
	m := NewMetrics()
	observer := m.OutboxObserver()
	for _, eventType := range []string{"memory_created", "memory_updated", "memory_pinned", "memory_unpinned", "memory_enabled", "memory_unknown"} {
		observer.ObserveProjection(context.Background(), service.OutboxProjectionObservation{
			Status:    "projected",
			EventType: eventType,
			TenantID:  "tenant-c",
		})
	}

	snap := m.Snapshot()
	if len(snap.EpisodicDisabledTotal) != 0 || len(snap.EpisodicDeletedTotal) != 0 {
		t.Fatalf("lifecycle counters bled from non-matching events: disabled=%+v deleted=%+v",
			snap.EpisodicDisabledTotal, snap.EpisodicDeletedTotal)
	}
}

func TestOutboxObserver_LifecycleBucketsByTenantID(t *testing.T) {
	m := NewMetrics()
	observer := m.OutboxObserver()
	observer.ObserveProjection(context.Background(), service.OutboxProjectionObservation{
		EventType: "memory_disabled",
		TenantID:  "alpha",
	})
	observer.ObserveProjection(context.Background(), service.OutboxProjectionObservation{
		EventType: "memory_disabled",
		TenantID:  "alpha",
	})
	observer.ObserveProjection(context.Background(), service.OutboxProjectionObservation{
		EventType: "memory_disabled",
		TenantID:  "beta",
	})

	bucketAlpha := service.TenantBucket("alpha")
	bucketBeta := service.TenantBucket("beta")
	snap := m.Snapshot()
	wantAlpha := uint64(2)
	if bucketAlpha == bucketBeta {
		// alpha and beta collided into the same bucket — total should be 3 there.
		wantAlpha = 3
	}
	if got := snap.EpisodicDisabledTotal[bucketAlpha]; got != wantAlpha {
		t.Fatalf("episodic_disabled[%s] = %d, want %d", bucketAlpha, got, wantAlpha)
	}
	if bucketAlpha != bucketBeta {
		if got := snap.EpisodicDisabledTotal[bucketBeta]; got != 1 {
			t.Fatalf("episodic_disabled[%s] = %d, want 1", bucketBeta, got)
		}
	}
}

// ---- trace_dropped wiring (sink-as-source-of-truth) ----

func TestMetrics_TraceDroppedReadsFromSource(t *testing.T) {
	m := NewMetrics()
	m.SetTraceDropSource(func() service.TraceDroppedSnapshot {
		return service.TraceDroppedSnapshot{BufferFull: 5, DBError: 3, Shutdown: 1}
	})
	body := handlerBody(t, m)
	for _, want := range []string{
		`trace_dropped_total{reason="buffer_full"} 5`,
		`trace_dropped_total{reason="db_error"} 3`,
		`trace_dropped_total{reason="shutdown"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q\n%s", want, body)
		}
	}
}

func TestMetrics_TraceDroppedDefaultSourceIsZero(t *testing.T) {
	m := NewMetrics()
	body := handlerBody(t, m)
	for _, want := range []string{
		`trace_dropped_total{reason="buffer_full"} 0`,
		`trace_dropped_total{reason="db_error"} 0`,
		`trace_dropped_total{reason="shutdown"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q with default source\n%s", want, body)
		}
	}
}

// ---- concurrency safety ----

func TestMetrics_AddRecallReturned_RaceSafe(t *testing.T) {
	t.Parallel()
	m := NewMetrics()
	const goroutines = 100
	const incrementsPerGoroutine = 100
	buckets := []string{"00", "01", "02", "03", "04"}

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		bucket := buckets[g%len(buckets)]
		go func(b string) {
			defer wg.Done()
			for i := 0; i < incrementsPerGoroutine; i++ {
				m.AddRecallReturned(b, 1)
			}
		}(bucket)
	}
	wg.Wait()

	snap := m.Snapshot()
	var total uint64
	for _, v := range snap.RecallReturnedTotal {
		total += v
	}
	if total != goroutines*incrementsPerGoroutine {
		t.Fatalf("race-safe total = %d, want %d", total, goroutines*incrementsPerGoroutine)
	}
}

// handlerBody is a small helper that captures the metrics handler body.
func handlerBody(t *testing.T, m *Metrics) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(recorder, req)
	return recorder.Body.String()
}
