package observability

import (
	"context"
	"time"

	"github.com/costa92/llm-agent-memory-gateway/internal/service"
)

// TraceSinkEmitter adapts a service.DecisionTraceSink to the service.TraceEmitter
// interface. It is composed into the existing emitter chain via
// observability.ComposeTraceEmitters so it picks up every Emit call site without
// any change to internal/service/service.go.
//
// The adapter pulls a small set of well-known keys (tenant_id, request_id,
// memory_id, version, reason) into typed columns on TraceRow. The `mode` key is
// folded into Reason as a back-compat shim for existing promote_decided
// emissions that pre-date the typed schema. Everything else lands in Payload.
type TraceSinkEmitter struct {
	sink    service.DecisionTraceSink
	emitter string
	now     func() time.Time
}

// NewTraceSinkEmitter constructs an adapter. A nil clock falls back to
// time.Now; a nil sink is tolerated but every Emit becomes a no-op.
func NewTraceSinkEmitter(sink service.DecisionTraceSink, emitter string, now func() time.Time) *TraceSinkEmitter {
	if now == nil {
		now = time.Now
	}
	return &TraceSinkEmitter{sink: sink, emitter: emitter, now: now}
}

// Emit constructs a TraceRow from the emitted fields and forwards it to the
// underlying sink. Errors from the sink are swallowed by design — the request
// path must not observe persistence failures.
func (e *TraceSinkEmitter) Emit(ctx context.Context, stage string, fields map[string]any) {
	if e == nil || e.sink == nil {
		return
	}
	row := service.TraceRow{
		Stage:     stage,
		EmittedAt: e.now(),
		Emitter:   e.emitter,
		Payload:   make(map[string]any, len(fields)),
	}
	for k, v := range fields {
		switch k {
		case "tenant_id":
			row.TenantID, _ = v.(string)
		case "request_id":
			row.RequestID, _ = v.(string)
		case "memory_id":
			row.MemoryID, _ = v.(string)
		case "version":
			// Accept int64 directly; tolerate int as well for ergonomic call sites.
			switch t := v.(type) {
			case int64:
				row.Version = t
			case int:
				row.Version = int64(t)
			}
		case "reason":
			row.Reason, _ = v.(string)
		case "mode":
			// Back-compat: existing promote_decided emissions use `mode` and
			// pre-date the typed `reason` column. If a call site supplies
			// only `mode`, fold it into Reason. An explicit `reason` (set
			// earlier or later in the loop) takes precedence — we only fill
			// from `mode` when Reason is still zero. Always preserve `mode`
			// in Payload as well so downstream queries can still see it.
			if row.Reason == "" {
				row.Reason, _ = v.(string)
			}
			row.Payload[k] = v
		default:
			row.Payload[k] = v
		}
	}
	// The `mode` fallback above runs before an explicit `reason` may be seen
	// (Go map iteration order is unspecified). Repair the precedence here.
	if reason, ok := fields["reason"].(string); ok && reason != "" {
		row.Reason = reason
	}
	_ = e.sink.Record(ctx, row)
}
