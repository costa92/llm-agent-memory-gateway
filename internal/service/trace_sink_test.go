package service

import (
	"context"
	"testing"
	"time"
)

func TestDecisionTraceSink_NopRecordReturnsNil(t *testing.T) {
	sink := NewNopDecisionTraceSink()
	row := TraceRow{
		TenantID:  "tenant-a",
		RequestID: "req-1",
		Stage:     "recalled",
		Reason:    "ok",
		MemoryID:  "mem_1",
		Version:   7,
		EmittedAt: time.Unix(1700000000, 0).UTC(),
		Emitter:   "gateway",
		Payload:   map[string]any{"k": "v"},
	}
	if err := sink.Record(context.Background(), row); err != nil {
		t.Fatalf("nop sink Record returned err=%v, want nil", err)
	}
}

func TestDecisionTraceSink_NopAcceptsEmptyRow(t *testing.T) {
	sink := NewNopDecisionTraceSink()
	if err := sink.Record(context.Background(), TraceRow{}); err != nil {
		t.Fatalf("nop sink Record empty row returned err=%v, want nil", err)
	}
}

func TestDecisionTraceSink_InterfaceShape(t *testing.T) {
	// Compile-time check: nopDecisionTraceSink must satisfy DecisionTraceSink.
	var _ DecisionTraceSink = nopDecisionTraceSink{}
	// Constructor returns the interface.
	var sink DecisionTraceSink = NewNopDecisionTraceSink()
	if sink == nil {
		t.Fatal("NewNopDecisionTraceSink returned nil")
	}
}
