package service

import (
	"context"
	"time"
)

// TraceRow is the value type persisted by a DecisionTraceSink. The reason column
// is intentionally free-form at v1; the enum is frozen at M8.
type TraceRow struct {
	TenantID  string
	RequestID string
	Stage     string
	Reason    string
	MemoryID  string
	Version   int64
	EmittedAt time.Time
	Emitter   string
	Payload   map[string]any
}

// DecisionTraceSink is the seam through which decision-trace rows are persisted.
// Implementations are expected to be non-blocking (best-effort) on the request
// path; durability and batching are an implementation concern.
type DecisionTraceSink interface {
	Record(ctx context.Context, row TraceRow) error
}

type nopDecisionTraceSink struct{}

func (nopDecisionTraceSink) Record(context.Context, TraceRow) error { return nil }

// NewNopDecisionTraceSink returns a sink that discards every row. Useful as a
// default when persistence is not configured and in tests.
func NewNopDecisionTraceSink() DecisionTraceSink { return nopDecisionTraceSink{} }
