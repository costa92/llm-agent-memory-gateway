package observability

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/costa92/llm-agent-memory-gateway/internal/service"
)

// recordingSink captures every row passed to Record for assertion.
type recordingSink struct {
	mu   sync.Mutex
	rows []service.TraceRow
	err  error
}

func (s *recordingSink) Record(_ context.Context, row service.TraceRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append(s.rows, row)
	return s.err
}

func (s *recordingSink) snapshot() []service.TraceRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]service.TraceRow, len(s.rows))
	copy(out, s.rows)
	return out
}

func TestTraceSinkEmitter_ImplementsTraceEmitter(t *testing.T) {
	var _ service.TraceEmitter = (*TraceSinkEmitter)(nil)
}

func TestTraceSinkEmitter_ExtractsKnownFields(t *testing.T) {
	sink := &recordingSink{}
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	emitter := NewTraceSinkEmitter(sink, "gateway", func() time.Time { return now })

	emitter.Emit(context.Background(), "recalled", map[string]any{
		"tenant_id":  "tenant-a",
		"request_id": "req-1",
		"memory_id":  "mem_42",
		"version":    int64(7),
		"reason":     "match",
		"count":      3,
	})

	rows := sink.snapshot()
	if len(rows) != 1 {
		t.Fatalf("rows=%d, want 1", len(rows))
	}
	row := rows[0]
	if row.Stage != "recalled" {
		t.Fatalf("Stage=%q, want recalled", row.Stage)
	}
	if row.TenantID != "tenant-a" {
		t.Fatalf("TenantID=%q", row.TenantID)
	}
	if row.RequestID != "req-1" {
		t.Fatalf("RequestID=%q", row.RequestID)
	}
	if row.MemoryID != "mem_42" {
		t.Fatalf("MemoryID=%q", row.MemoryID)
	}
	if row.Version != 7 {
		t.Fatalf("Version=%d", row.Version)
	}
	if row.Reason != "match" {
		t.Fatalf("Reason=%q", row.Reason)
	}
	if row.Emitter != "gateway" {
		t.Fatalf("Emitter=%q", row.Emitter)
	}
	if !row.EmittedAt.Equal(now) {
		t.Fatalf("EmittedAt=%v, want %v", row.EmittedAt, now)
	}
	// Known keys are stripped from Payload; everything else is preserved.
	if _, present := row.Payload["tenant_id"]; present {
		t.Fatalf("Payload should not contain tenant_id, got %v", row.Payload)
	}
	if got, ok := row.Payload["count"].(int); !ok || got != 3 {
		t.Fatalf("Payload[count]=%v, want 3", row.Payload["count"])
	}
}

func TestTraceSinkEmitter_ModeFallsBackIntoReason(t *testing.T) {
	// Existing promote_decided call sites in service.go pass `mode`, not
	// `reason`. The adapter must back-compat this without editing service.go.
	sink := &recordingSink{}
	emitter := NewTraceSinkEmitter(sink, "gateway", time.Now)

	emitter.Emit(context.Background(), "promote_decided", map[string]any{
		"tenant_id":  "tenant-a",
		"session_id": "sess-1",
		"mode":       "deferred",
	})

	rows := sink.snapshot()
	if len(rows) != 1 {
		t.Fatalf("rows=%d", len(rows))
	}
	if rows[0].Reason != "deferred" {
		t.Fatalf("Reason=%q, want deferred (from mode fallback)", rows[0].Reason)
	}
	// session_id is unknown to the typed columns — it must land in Payload.
	if got, _ := rows[0].Payload["session_id"].(string); got != "sess-1" {
		t.Fatalf("Payload[session_id]=%v, want sess-1", rows[0].Payload["session_id"])
	}
}

func TestTraceSinkEmitter_ExplicitReasonWinsOverMode(t *testing.T) {
	// If a call site supplies both `reason` and `mode`, the explicit reason
	// must take precedence — the fallback only applies when reason is empty.
	sink := &recordingSink{}
	emitter := NewTraceSinkEmitter(sink, "gateway", time.Now)

	emitter.Emit(context.Background(), "promote_decided", map[string]any{
		"reason": "primary",
		"mode":   "fallback",
	})

	rows := sink.snapshot()
	if len(rows) != 1 || rows[0].Reason != "primary" {
		t.Fatalf("Reason=%q, want primary", rows[0].Reason)
	}
}

func TestTraceSinkEmitter_EmptyFieldsProducesRow(t *testing.T) {
	sink := &recordingSink{}
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	emitter := NewTraceSinkEmitter(sink, "gateway", func() time.Time { return now })

	emitter.Emit(context.Background(), "selected", nil)

	rows := sink.snapshot()
	if len(rows) != 1 {
		t.Fatalf("rows=%d", len(rows))
	}
	if rows[0].Stage != "selected" {
		t.Fatalf("Stage=%q", rows[0].Stage)
	}
	if !rows[0].EmittedAt.Equal(now) {
		t.Fatalf("EmittedAt=%v", rows[0].EmittedAt)
	}
}

func TestTraceSinkEmitter_NowDefaultsToTimeNow(t *testing.T) {
	// A nil clock should fall back to time.Now (constructor convenience).
	sink := &recordingSink{}
	emitter := NewTraceSinkEmitter(sink, "gateway", nil)

	before := time.Now()
	emitter.Emit(context.Background(), "recalled", nil)
	after := time.Now()

	rows := sink.snapshot()
	if len(rows) != 1 {
		t.Fatalf("rows=%d", len(rows))
	}
	got := rows[0].EmittedAt
	if got.Before(before) || got.After(after) {
		t.Fatalf("EmittedAt=%v not within [%v,%v]", got, before, after)
	}
}
