package observability

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-memory-gateway/internal/service"
)

func TestWorkingLifecycleObserver_IncrementsCountersByObservationCounts(t *testing.T) {
	m := NewMetrics()
	obs := m.WorkingLifecycleObserver()
	obs.ObserveWorkingLifecycle(context.Background(), service.WorkingLifecycleObservation{
		TenantID:         "tenant_a",
		Mode:             "expire_working",
		Expired:          3,
		DroppedBeforeUse: 2,
	})

	bucket := service.TenantBucket("tenant_a")
	snap := m.Snapshot()
	if got := snap.WorkingExpiredTotal[bucket]; got != 3 {
		t.Fatalf("working_expired_total[%s] = %d, want 3", bucket, got)
	}
	if got := snap.WorkingDroppedBeforeUseTotal[bucket]; got != 2 {
		t.Fatalf("working_dropped_before_use_total[%s] = %d, want 2", bucket, got)
	}
}

func TestWorkingLifecycleObserver_ZeroCountsAreNoOp(t *testing.T) {
	m := NewMetrics()
	obs := m.WorkingLifecycleObserver()
	obs.ObserveWorkingLifecycle(context.Background(), service.WorkingLifecycleObservation{
		TenantID:         "tenant_a",
		Mode:             "promote_and_expire",
		Expired:          0,
		DroppedBeforeUse: 0,
	})

	snap := m.Snapshot()
	if len(snap.WorkingExpiredTotal) != 0 {
		t.Fatalf("working_expired_total should have no buckets for a zero observation, got %v", snap.WorkingExpiredTotal)
	}
	if len(snap.WorkingDroppedBeforeUseTotal) != 0 {
		t.Fatalf("working_dropped_before_use_total should have no buckets for a zero observation, got %v", snap.WorkingDroppedBeforeUseTotal)
	}
}

func TestWorkingLifecycleObserver_AccumulatesAcrossObservations(t *testing.T) {
	m := NewMetrics()
	obs := m.WorkingLifecycleObserver()
	for i := 0; i < 3; i++ {
		obs.ObserveWorkingLifecycle(context.Background(), service.WorkingLifecycleObservation{
			TenantID: "tenant_a",
			Expired:  2,
		})
	}
	bucket := service.TenantBucket("tenant_a")
	if got := m.Snapshot().WorkingExpiredTotal[bucket]; got != 6 {
		t.Fatalf("working_expired_total[%s] = %d, want 6 (2*3)", bucket, got)
	}
}

func TestWorkingLifecycleObserver_NilMetricsIsSafe(t *testing.T) {
	var o workingLifecycleMetricsObserver // nil metrics
	o.ObserveWorkingLifecycle(context.Background(), service.WorkingLifecycleObservation{TenantID: "t", Expired: 1})
	// must not panic
}
