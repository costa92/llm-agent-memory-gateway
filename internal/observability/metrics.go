package observability

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/costa92/llm-agent-memory-gateway/internal/service"
)

type Snapshot struct {
	RecallL1HitTotal               int64
	RecallL2HitTotal               int64
	RecallOriginTotal              int64
	RecallStaleServedTotal         int64
	RecallCacheFillTotal           int64
	RecallInvalidationTotal        int64
	OutboxProjectionProjectedTotal int64
	OutboxProjectionStaleTotal     int64
	OutboxProjectionFailedTotal    int64
	OutboxProjectionIgnoredTotal   int64

	// M7 validation counters — one map entry per tenant_bucket. The bucket
	// dimension is the only label these counters carry; see
	// docs/superpowers/plans/2026-05-27-m7-validation-telemetry-and-trace.md
	// "Open Decisions / Cardinality".
	EmbeddingRequestTotal   map[string]uint64
	EmbeddingAppliedTotal   map[string]uint64
	EmbeddingTokensTotal    map[string]uint64
	EmbeddingCostTotal      map[string]uint64
	MemoryStorageBytesTotal map[string]uint64 // gauge — last write wins per bucket
	VectorStorageBytesTotal map[string]uint64 // gauge — last write wins per bucket
	EpisodicDisabledTotal   map[string]uint64
	EpisodicDeletedTotal    map[string]uint64
	RecallReturnedTotal     map[string]uint64
	RecallSelectedTotal     map[string]uint64

	// M8 working-memory lifecycle counters — one map entry per tenant_bucket
	// (D7). Source: the session closer's WorkingLifecycleObservation.
	WorkingExpiredTotal          map[string]uint64
	WorkingDroppedBeforeUseTotal map[string]uint64

	// TraceDropped is read from the injected source (sink-as-source-of-truth)
	// so the gateway does not double-book this counter. Zero values when no
	// source is wired.
	TraceDropped service.TraceDroppedSnapshot

	// StorageCronFailuresTotal counts ticks of the storage-bytes cron whose
	// query returned an error. Operational telemetry for the cron itself —
	// not labelled by tenant_bucket because the failure is global to the
	// query.
	StorageCronFailuresTotal uint64
}

type Metrics struct {
	recallL1Hit        atomic.Int64
	recallL2Hit        atomic.Int64
	recallOrigin       atomic.Int64
	recallStaleServed  atomic.Int64
	recallCacheFill    atomic.Int64
	recallInvalidation atomic.Int64

	outboxProjected atomic.Int64
	outboxStale     atomic.Int64
	outboxFailed    atomic.Int64
	outboxIgnored   atomic.Int64

	storageCronFailures atomic.Uint64

	// Per-bucket counters. mu guards map structure; the *atomic.Uint64 values
	// themselves are lock-free once their map entry exists, so the hot path
	// after first insert is a read-lock + atomic.Add.
	mu                 sync.RWMutex
	embeddingRequest   map[string]*atomic.Uint64
	embeddingApplied   map[string]*atomic.Uint64
	embeddingTokens    map[string]*atomic.Uint64
	embeddingCost      map[string]*atomic.Uint64
	memoryStorageBytes map[string]*atomic.Uint64
	vectorStorageBytes map[string]*atomic.Uint64
	episodicDisabled   map[string]*atomic.Uint64
	episodicDeleted    map[string]*atomic.Uint64
	recallReturned     map[string]*atomic.Uint64
	recallSelected     map[string]*atomic.Uint64

	workingExpired          map[string]*atomic.Uint64
	workingDroppedBeforeUse map[string]*atomic.Uint64

	// traceDropSource returns the live drop counters from the trace sink. The
	// default returns a zero snapshot so handler exposition lines remain
	// present (and zero) even when no sink is wired.
	traceDropSourceMu sync.RWMutex
	traceDropSource   func() service.TraceDroppedSnapshot
}

func NewMetrics() *Metrics {
	return &Metrics{
		embeddingRequest:   make(map[string]*atomic.Uint64),
		embeddingApplied:   make(map[string]*atomic.Uint64),
		embeddingTokens:    make(map[string]*atomic.Uint64),
		embeddingCost:      make(map[string]*atomic.Uint64),
		memoryStorageBytes: make(map[string]*atomic.Uint64),
		vectorStorageBytes: make(map[string]*atomic.Uint64),
		episodicDisabled:   make(map[string]*atomic.Uint64),
		episodicDeleted:    make(map[string]*atomic.Uint64),
		recallReturned:     make(map[string]*atomic.Uint64),
		recallSelected:     make(map[string]*atomic.Uint64),

		workingExpired:          make(map[string]*atomic.Uint64),
		workingDroppedBeforeUse: make(map[string]*atomic.Uint64),

		traceDropSource: func() service.TraceDroppedSnapshot { return service.TraceDroppedSnapshot{} },
	}
}

func (m *Metrics) AddRecallL1Hit()        { m.recallL1Hit.Add(1) }
func (m *Metrics) AddRecallL2Hit()        { m.recallL2Hit.Add(1) }
func (m *Metrics) AddRecallOrigin()       { m.recallOrigin.Add(1) }
func (m *Metrics) AddRecallStaleServed()  { m.recallStaleServed.Add(1) }
func (m *Metrics) AddRecallCacheFill()    { m.recallCacheFill.Add(1) }
func (m *Metrics) AddRecallInvalidation() { m.recallInvalidation.Add(1) }
func (m *Metrics) AddOutboxProjected()    { m.outboxProjected.Add(1) }
func (m *Metrics) AddOutboxStale()        { m.outboxStale.Add(1) }
func (m *Metrics) AddOutboxFailed()       { m.outboxFailed.Add(1) }
func (m *Metrics) AddOutboxIgnored()      { m.outboxIgnored.Add(1) }

// ---- M7 per-bucket counters ----

func (m *Metrics) AddEmbeddingRequest(tenantBucket string) {
	m.addBucket(m.embeddingRequest, tenantBucket, 1)
}
func (m *Metrics) AddEmbeddingApplied(tenantBucket string) {
	m.addBucket(m.embeddingApplied, tenantBucket, 1)
}
func (m *Metrics) AddEmbeddingTokens(tenantBucket string, n uint64) {
	m.addBucket(m.embeddingTokens, tenantBucket, n)
}
func (m *Metrics) AddEmbeddingCost(tenantBucket string, micros uint64) {
	m.addBucket(m.embeddingCost, tenantBucket, micros)
}
func (m *Metrics) SetMemoryStorageBytes(tenantBucket string, b uint64) {
	m.setBucket(m.memoryStorageBytes, tenantBucket, b)
}
func (m *Metrics) SetVectorStorageBytes(tenantBucket string, b uint64) {
	m.setBucket(m.vectorStorageBytes, tenantBucket, b)
}
func (m *Metrics) AddEpisodicDisabled(tenantBucket string) {
	m.addBucket(m.episodicDisabled, tenantBucket, 1)
}
func (m *Metrics) AddEpisodicDeleted(tenantBucket string) {
	m.addBucket(m.episodicDeleted, tenantBucket, 1)
}
func (m *Metrics) AddRecallReturned(tenantBucket string, n uint64) {
	m.addBucket(m.recallReturned, tenantBucket, n)
}
func (m *Metrics) AddRecallSelected(tenantBucket string, n uint64) {
	m.addBucket(m.recallSelected, tenantBucket, n)
}
func (m *Metrics) AddWorkingExpired(tenantBucket string, n uint64) {
	m.addBucket(m.workingExpired, tenantBucket, n)
}
func (m *Metrics) AddWorkingDroppedBeforeUse(tenantBucket string, n uint64) {
	m.addBucket(m.workingDroppedBeforeUse, tenantBucket, n)
}

// AddStorageCronFailure increments the operational counter for storage-bytes
// cron failures (Task 7). Unlike the 10 validation counters this carries no
// tenant_bucket label — the failure is global to the cron tick.
func (m *Metrics) AddStorageCronFailure() { m.storageCronFailures.Add(1) }

// SetTraceDropSource wires the sink's drop counters as the source of truth for
// the trace_dropped_total exposition. Passing nil restores the zero-snapshot
// default. Safe to call concurrently with Handler().
func (m *Metrics) SetTraceDropSource(fn func() service.TraceDroppedSnapshot) {
	m.traceDropSourceMu.Lock()
	defer m.traceDropSourceMu.Unlock()
	if fn == nil {
		m.traceDropSource = func() service.TraceDroppedSnapshot { return service.TraceDroppedSnapshot{} }
		return
	}
	m.traceDropSource = fn
}

// addBucket is the lock-light per-bucket increment. The fast path takes a
// read-lock, finds the counter, and atomically adds; first-touch on a bucket
// upgrades to a write-lock.
func (m *Metrics) addBucket(buckets map[string]*atomic.Uint64, key string, delta uint64) {
	m.mu.RLock()
	v, ok := buckets[key]
	m.mu.RUnlock()
	if ok {
		v.Add(delta)
		return
	}
	m.mu.Lock()
	// Re-check under write-lock — another goroutine may have inserted.
	if v, ok = buckets[key]; !ok {
		v = new(atomic.Uint64)
		buckets[key] = v
	}
	m.mu.Unlock()
	v.Add(delta)
}

func (m *Metrics) setBucket(buckets map[string]*atomic.Uint64, key string, value uint64) {
	m.mu.RLock()
	v, ok := buckets[key]
	m.mu.RUnlock()
	if ok {
		v.Store(value)
		return
	}
	m.mu.Lock()
	if v, ok = buckets[key]; !ok {
		v = new(atomic.Uint64)
		buckets[key] = v
	}
	m.mu.Unlock()
	v.Store(value)
}

func (m *Metrics) Snapshot() Snapshot {
	snap := Snapshot{
		RecallL1HitTotal:               m.recallL1Hit.Load(),
		RecallL2HitTotal:               m.recallL2Hit.Load(),
		RecallOriginTotal:              m.recallOrigin.Load(),
		RecallStaleServedTotal:         m.recallStaleServed.Load(),
		RecallCacheFillTotal:           m.recallCacheFill.Load(),
		RecallInvalidationTotal:        m.recallInvalidation.Load(),
		OutboxProjectionProjectedTotal: m.outboxProjected.Load(),
		OutboxProjectionStaleTotal:     m.outboxStale.Load(),
		OutboxProjectionFailedTotal:    m.outboxFailed.Load(),
		OutboxProjectionIgnoredTotal:   m.outboxIgnored.Load(),
		StorageCronFailuresTotal:       m.storageCronFailures.Load(),
	}
	m.mu.RLock()
	snap.EmbeddingRequestTotal = copyBuckets(m.embeddingRequest)
	snap.EmbeddingAppliedTotal = copyBuckets(m.embeddingApplied)
	snap.EmbeddingTokensTotal = copyBuckets(m.embeddingTokens)
	snap.EmbeddingCostTotal = copyBuckets(m.embeddingCost)
	snap.MemoryStorageBytesTotal = copyBuckets(m.memoryStorageBytes)
	snap.VectorStorageBytesTotal = copyBuckets(m.vectorStorageBytes)
	snap.EpisodicDisabledTotal = copyBuckets(m.episodicDisabled)
	snap.EpisodicDeletedTotal = copyBuckets(m.episodicDeleted)
	snap.RecallReturnedTotal = copyBuckets(m.recallReturned)
	snap.RecallSelectedTotal = copyBuckets(m.recallSelected)
	snap.WorkingExpiredTotal = copyBuckets(m.workingExpired)
	snap.WorkingDroppedBeforeUseTotal = copyBuckets(m.workingDroppedBeforeUse)
	m.mu.RUnlock()

	m.traceDropSourceMu.RLock()
	src := m.traceDropSource
	m.traceDropSourceMu.RUnlock()
	if src != nil {
		snap.TraceDropped = src()
	}
	return snap
}

func copyBuckets(buckets map[string]*atomic.Uint64) map[string]uint64 {
	out := make(map[string]uint64, len(buckets))
	for k, v := range buckets {
		out[k] = v.Load()
	}
	return out
}

func (m *Metrics) TraceEmitter() service.TraceEmitter {
	return traceMetricsEmitter{metrics: m}
}

func (m *Metrics) RecallObserver() service.RecallObserver {
	return recallMetricsObserver{metrics: m}
}

func (m *Metrics) RecallCacheObserver() service.RecallCacheObserver {
	return recallCacheMetricsObserver{metrics: m}
}

func (m *Metrics) OutboxObserver() service.OutboxProjectionObserver {
	return outboxMetricsObserver{metrics: m}
}

func (m *Metrics) WorkingLifecycleObserver() service.WorkingLifecycleObserver {
	return workingLifecycleMetricsObserver{metrics: m}
}

func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		snap := m.Snapshot()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		lines := []string{
			fmt.Sprintf("recall_l1_hit_total %d", snap.RecallL1HitTotal),
			fmt.Sprintf("recall_l2_hit_total %d", snap.RecallL2HitTotal),
			fmt.Sprintf("recall_origin_total %d", snap.RecallOriginTotal),
			fmt.Sprintf("recall_stale_served_total %d", snap.RecallStaleServedTotal),
			fmt.Sprintf("recall_cache_fill_total %d", snap.RecallCacheFillTotal),
			fmt.Sprintf("recall_invalidation_total %d", snap.RecallInvalidationTotal),
			fmt.Sprintf("outbox_projection_projected_total %d", snap.OutboxProjectionProjectedTotal),
			fmt.Sprintf("outbox_projection_stale_total %d", snap.OutboxProjectionStaleTotal),
			fmt.Sprintf("outbox_projection_failed_total %d", snap.OutboxProjectionFailedTotal),
			fmt.Sprintf("outbox_projection_ignored_total %d", snap.OutboxProjectionIgnoredTotal),
		}

		// Per-bucket counter lines. Sorted keys keep the exposition stable for
		// test assertions and scrape diffs.
		lines = appendBucketLines(lines, "embedding_request_total", snap.EmbeddingRequestTotal)
		lines = appendBucketLines(lines, "embedding_applied_total", snap.EmbeddingAppliedTotal)
		lines = appendBucketLines(lines, "embedding_tokens_total", snap.EmbeddingTokensTotal)
		lines = appendBucketLines(lines, "embedding_cost_total", snap.EmbeddingCostTotal)
		lines = appendBucketLines(lines, "memory_storage_bytes_total", snap.MemoryStorageBytesTotal)
		lines = appendBucketLines(lines, "vector_storage_bytes_total", snap.VectorStorageBytesTotal)
		lines = appendBucketLines(lines, "episodic_disabled_total", snap.EpisodicDisabledTotal)
		lines = appendBucketLines(lines, "episodic_deleted_total", snap.EpisodicDeletedTotal)
		lines = appendBucketLines(lines, "recall_returned_total", snap.RecallReturnedTotal)
		lines = appendBucketLines(lines, "recall_selected_total", snap.RecallSelectedTotal)
		lines = appendBucketLines(lines, "working_expired_total", snap.WorkingExpiredTotal)
		lines = appendBucketLines(lines, "working_dropped_before_use_total", snap.WorkingDroppedBeforeUseTotal)

		// trace_dropped_total is read straight from the sink (source of truth).
		// The 3 reason values are bounded at compile time; always emit all
		// three so dashboards see a stable label set even at zero.
		lines = append(lines,
			fmt.Sprintf(`trace_dropped_total{reason="buffer_full"} %d`, snap.TraceDropped.BufferFull),
			fmt.Sprintf(`trace_dropped_total{reason="db_error"} %d`, snap.TraceDropped.DBError),
			fmt.Sprintf(`trace_dropped_total{reason="shutdown"} %d`, snap.TraceDropped.Shutdown),
			fmt.Sprintf("storage_cron_failures_total %d", snap.StorageCronFailuresTotal),
		)

		_, _ = fmt.Fprint(w, strings.Join(lines, "\n"))
	})
}

// appendBucketLines emits one `<name>{tenant_bucket="<bucket>"} <value>` line
// per bucket entry. Keys are sorted so output ordering is stable.
func appendBucketLines(lines []string, name string, buckets map[string]uint64) []string {
	if len(buckets) == 0 {
		return lines
	}
	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf(`%s{tenant_bucket=%q} %d`, name, k, buckets[k]))
	}
	return lines
}

type traceMetricsEmitter struct {
	metrics *Metrics
}

func (e traceMetricsEmitter) Emit(_ context.Context, stage string, fields map[string]any) {
	if e.metrics == nil {
		return
	}
	_ = stage
	_ = fields
}

type recallMetricsObserver struct {
	metrics *Metrics
}

func (o recallMetricsObserver) ObserveRecall(_ context.Context, obs service.RecallObservation) {
	if o.metrics == nil {
		return
	}
	switch obs.CacheLevel {
	case "l1_hit":
		o.metrics.AddRecallL1Hit()
	case "l2_hit":
		o.metrics.AddRecallL2Hit()
	case "origin":
		o.metrics.AddRecallOrigin()
	}
	if obs.StaleServed {
		o.metrics.AddRecallStaleServed()
	}

	// returned/selected validation counters. Bucket the tenant_id at the call
	// site (Open Decisions / Cardinality). Zero counts skip the increment so
	// zero-hit recalls do not allocate a bucket entry.
	if obs.Returned > 0 {
		o.metrics.AddRecallReturned(service.TenantBucket(obs.TenantID), uint64(obs.Returned))
	}
	if obs.Selected > 0 {
		o.metrics.AddRecallSelected(service.TenantBucket(obs.TenantID), uint64(obs.Selected))
	}
}

type outboxMetricsObserver struct {
	metrics *Metrics
}

type recallCacheMetricsObserver struct {
	metrics *Metrics
}

func (o recallCacheMetricsObserver) ObserveRecallCache(_ context.Context, obs service.RecallCacheObservation) {
	if o.metrics == nil {
		return
	}
	switch obs.Action {
	case "fill":
		o.metrics.AddRecallCacheFill()
	case "invalidate":
		o.metrics.AddRecallInvalidation()
	}
}

func (o outboxMetricsObserver) ObserveProjection(_ context.Context, obs service.OutboxProjectionObservation) {
	if o.metrics == nil {
		return
	}
	switch obs.Status {
	case "projected":
		o.metrics.AddOutboxProjected()
	case "stale":
		o.metrics.AddOutboxStale()
	case "failed":
		o.metrics.AddOutboxFailed()
	case "ignored":
		o.metrics.AddOutboxIgnored()
	}

	// Lifecycle counters fire once per observation that corresponds to a
	// memory_disabled / memory_deleted outbox event, regardless of the
	// projection Status. Bucket the tenant_id at the call site so the metric
	// stays within the cardinality budget (Open Decisions / Cardinality).
	switch obs.EventType {
	case "memory_disabled":
		o.metrics.AddEpisodicDisabled(service.TenantBucket(obs.TenantID))
	case "memory_deleted":
		o.metrics.AddEpisodicDeleted(service.TenantBucket(obs.TenantID))
	}
}

// workingLifecycleMetricsObserver maps the session closer's lifecycle
// observations onto the per-tenant_bucket working_* counters (D7). The
// observation already carries the per-mode Expired / DroppedBeforeUse counts;
// bucket the tenant_id at the call site to stay within the cardinality budget.
type workingLifecycleMetricsObserver struct {
	metrics *Metrics
}

func (o workingLifecycleMetricsObserver) ObserveWorkingLifecycle(_ context.Context, obs service.WorkingLifecycleObservation) {
	if o.metrics == nil {
		return
	}
	bucket := service.TenantBucket(obs.TenantID)
	if obs.Expired > 0 {
		o.metrics.AddWorkingExpired(bucket, uint64(obs.Expired))
	}
	if obs.DroppedBeforeUse > 0 {
		o.metrics.AddWorkingDroppedBeforeUse(bucket, uint64(obs.DroppedBeforeUse))
	}
}

type composedTraceEmitter struct {
	emitters []service.TraceEmitter
}

func ComposeTraceEmitters(emitters ...service.TraceEmitter) service.TraceEmitter {
	filtered := make([]service.TraceEmitter, 0, len(emitters))
	for _, emitter := range emitters {
		if emitter != nil {
			filtered = append(filtered, emitter)
		}
	}
	return composedTraceEmitter{emitters: filtered}
}

func (e composedTraceEmitter) Emit(ctx context.Context, stage string, fields map[string]any) {
	for _, emitter := range e.emitters {
		emitter.Emit(ctx, stage, fields)
	}
}
