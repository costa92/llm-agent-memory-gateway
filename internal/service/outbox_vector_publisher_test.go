package service

import (
	"context"
	"errors"
	"testing"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	pgmemory "github.com/costa92/llm-agent-memory-postgres/postgres"
)

type fakeVectorProjector struct {
	upserts []corememory.MemoryRecord
	removes []string
	err     error
}

func (f *fakeVectorProjector) ProjectUpsert(_ context.Context, _ authz.Scope, record corememory.MemoryRecord) error {
	if f.err != nil {
		return f.err
	}
	f.upserts = append(f.upserts, record)
	return nil
}

func (f *fakeVectorProjector) ProjectRemove(_ context.Context, _ authz.Scope, memoryID string) error {
	if f.err != nil {
		return f.err
	}
	f.removes = append(f.removes, memoryID)
	return nil
}

type fakePublisherRecordStore struct {
	record corememory.MemoryRecord
	err    error
	calls  int
}

func (f *fakePublisherRecordStore) GetRecord(_ context.Context, _ string, _ string) (corememory.MemoryRecord, error) {
	f.calls++
	if f.err != nil {
		return corememory.MemoryRecord{}, f.err
	}
	return f.record, nil
}

func (f *fakePublisherRecordStore) GetRecordIncludingHidden(_ context.Context, _ string, _ string) (corememory.MemoryRecord, error) {
	if f.err != nil {
		return corememory.MemoryRecord{}, f.err
	}
	return f.record, nil
}

func (f *fakePublisherRecordStore) WriteRecord(context.Context, corememory.WriteRecordInput) (corememory.WriteRecordResult, error) {
	return corememory.WriteRecordResult{}, nil
}

func (f *fakePublisherRecordStore) PatchRecord(context.Context, corememory.PatchRecordInput) (corememory.PatchRecordResult, error) {
	return corememory.PatchRecordResult{}, nil
}

func (f *fakePublisherRecordStore) DeleteRecord(context.Context, corememory.DeleteRecordInput) (corememory.DeleteRecordResult, error) {
	return corememory.DeleteRecordResult{}, nil
}

func (f *fakePublisherRecordStore) PinRecord(context.Context, corememory.PinRecordInput) (corememory.PinRecordResult, error) {
	return corememory.PinRecordResult{}, nil
}

func (f *fakePublisherRecordStore) DisableRecord(context.Context, corememory.DisableRecordInput) (corememory.DisableRecordResult, error) {
	return corememory.DisableRecordResult{}, nil
}

type fakeOutboxObserver struct {
	events []OutboxProjectionObservation
}

func (f *fakeOutboxObserver) ObserveProjection(_ context.Context, obs OutboxProjectionObservation) {
	f.events = append(f.events, obs)
}

func TestOutboxVectorPublisher_PublishUpsertsMutationEvents(t *testing.T) {
	projector := &fakeVectorProjector{}
	records := &fakePublisherRecordStore{record: corememory.MemoryRecord{MemoryID: "mem_1", TenantID: "tenant-a", UserID: "user-a", Content: "remember current", Version: 3}}
	observer := &fakeOutboxObserver{}
	publisher := NewOutboxVectorPublisher(records, projector, observer)

	for _, eventType := range []string{"memory_created", "memory_updated", "memory_pinned", "memory_unpinned", "memory_enabled"} {
		t.Run(eventType, func(t *testing.T) {
			err := publisher.Publish(context.Background(), corememory.OutboxMessage{
				EventType: eventType,
				MemoryID:  "mem_1",
				TenantID:  "tenant-a",
				Version:   3,
				Record: corememory.MemoryRecord{
					MemoryID: "mem_1",
					TenantID: "tenant-a",
					Content:  "remember this",
					Version:  3,
				},
			})
			if err != nil {
				t.Fatalf("Publish() error = %v", err)
			}
		})
	}

	if len(projector.upserts) != 5 {
		t.Fatalf("upserts = %d, want 5", len(projector.upserts))
	}
	for _, upsert := range projector.upserts {
		if upsert.Content != "remember current" {
			t.Fatalf("upsert content = %q, want current truth-source content", upsert.Content)
		}
	}
	if len(projector.removes) != 0 {
		t.Fatalf("removes = %v, want none", projector.removes)
	}
	if len(observer.events) != 5 || observer.events[0].Status != "projected" {
		t.Fatalf("observer events = %+v", observer.events)
	}
}

func TestOutboxVectorPublisher_TreatsMissingTruthSourceUpsertAsStale(t *testing.T) {
	projector := &fakeVectorProjector{}
	records := &fakePublisherRecordStore{err: pgmemory.ErrNotFound}
	observer := &fakeOutboxObserver{}
	publisher := NewOutboxVectorPublisher(records, projector, observer)

	err := publisher.Publish(context.Background(), corememory.OutboxMessage{
		EventType: "memory_updated",
		MemoryID:  "mem_1",
		TenantID:  "tenant-a",
		Version:   3,
		Record: corememory.MemoryRecord{
			MemoryID: "mem_1",
			TenantID: "tenant-a",
			Version:  3,
		},
	})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if len(projector.upserts) != 0 || len(projector.removes) != 0 {
		t.Fatalf("projector = %+v", projector)
	}
	if len(observer.events) != 1 || observer.events[0].Status != "stale" || observer.events[0].Reason != "truth_source_missing" {
		t.Fatalf("observer events = %+v", observer.events)
	}
}

func TestOutboxVectorPublisher_PublishRemovesDisableAndDeleteEvents(t *testing.T) {
	projector := &fakeVectorProjector{}
	records := &fakePublisherRecordStore{err: pgmemory.ErrNotFound}
	observer := &fakeOutboxObserver{}
	publisher := NewOutboxVectorPublisher(records, projector, observer)

	for _, eventType := range []string{"memory_disabled", "memory_deleted"} {
		t.Run(eventType, func(t *testing.T) {
			err := publisher.Publish(context.Background(), corememory.OutboxMessage{
				EventType: eventType,
				MemoryID:  "mem_1",
				TenantID:  "tenant-a",
				Version:   4,
				Record: corememory.MemoryRecord{
					MemoryID: "mem_1",
					TenantID: "tenant-a",
					Version:  4,
				},
			})
			if err != nil {
				t.Fatalf("Publish() error = %v", err)
			}
		})
	}

	if len(projector.upserts) != 0 {
		t.Fatalf("upserts = %v, want none", projector.upserts)
	}
	if len(projector.removes) != 2 {
		t.Fatalf("removes = %v, want 2 removes", projector.removes)
	}
	if len(observer.events) != 2 || observer.events[0].Reason != "remove" {
		t.Fatalf("observer events = %+v", observer.events)
	}
}

func TestOutboxVectorPublisher_IgnoresUnknownEventType(t *testing.T) {
	projector := &fakeVectorProjector{}
	observer := &fakeOutboxObserver{}
	publisher := NewOutboxVectorPublisher(&fakePublisherRecordStore{}, projector, observer)

	if err := publisher.Publish(context.Background(), corememory.OutboxMessage{
		EventType: "memory_unknown",
		MemoryID:  "mem_1",
		TenantID:  "tenant-a",
		Version:   5,
		Record: corememory.MemoryRecord{
			MemoryID: "mem_1",
			TenantID: "tenant-a",
			Version:  5,
		},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	if len(projector.upserts) != 0 || len(projector.removes) != 0 {
		t.Fatalf("projector = %+v", projector)
	}
	if len(observer.events) != 1 || observer.events[0].Status != "ignored" {
		t.Fatalf("observer events = %+v", observer.events)
	}
}

func TestOutboxVectorPublisher_SkipsStaleUpsertEvent(t *testing.T) {
	projector := &fakeVectorProjector{}
	records := &fakePublisherRecordStore{record: corememory.MemoryRecord{MemoryID: "mem_1", TenantID: "tenant-a", Version: 4}}
	observer := &fakeOutboxObserver{}
	publisher := NewOutboxVectorPublisher(records, projector, observer)

	if err := publisher.Publish(context.Background(), corememory.OutboxMessage{
		EventType: "memory_updated",
		MemoryID:  "mem_1",
		Version:   3,
		Record: corememory.MemoryRecord{
			MemoryID: "mem_1",
			TenantID: "tenant-a",
			Content:  "old value",
			Version:  3,
		},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	if len(projector.upserts) != 0 || len(projector.removes) != 0 {
		t.Fatalf("projector = %+v", projector)
	}
	if records.calls != 1 {
		t.Fatalf("GetRecord calls = %d, want 1", records.calls)
	}
	if len(observer.events) != 1 || observer.events[0].Status != "stale" {
		t.Fatalf("observer events = %+v", observer.events)
	}
}

func TestOutboxVectorPublisher_MemoryPromoted_NoProjectionAndObservesNoop(t *testing.T) {
	projector := &fakeVectorProjector{}
	observer := &fakeOutboxObserver{}
	publisher := NewOutboxVectorPublisher(&fakePublisherRecordStore{}, projector, observer)

	err := publisher.Publish(context.Background(), corememory.OutboxMessage{
		EventType: "memory_promoted",
		MemoryID:  "mem_prom",
		TenantID:  "tenant-a",
		Version:   7,
		Record: corememory.MemoryRecord{
			MemoryID: "mem_prom",
			TenantID: "tenant-a",
			Version:  7,
		},
	})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if len(projector.upserts) != 0 || len(projector.removes) != 0 {
		t.Fatalf("projector mutated = %+v (memory_promoted must not project)", projector)
	}
	if len(observer.events) != 1 {
		t.Fatalf("observer events = %d, want 1", len(observer.events))
	}
	if observer.events[0].Status != "promoted_noop" {
		t.Fatalf("status = %q, want promoted_noop", observer.events[0].Status)
	}
	if observer.events[0].EventVersion != 7 {
		t.Fatalf("event_version = %d, want 7", observer.events[0].EventVersion)
	}
}

func TestOutboxVectorPublisher_MemoryDedupeCollapsed_NoProjectionAndObservesCollapse(t *testing.T) {
	projector := &fakeVectorProjector{}
	observer := &fakeOutboxObserver{}
	publisher := NewOutboxVectorPublisher(&fakePublisherRecordStore{}, projector, observer)

	err := publisher.Publish(context.Background(), corememory.OutboxMessage{
		EventType: "memory_dedupe_collapsed",
		MemoryID:  "mem_loser",
		TenantID:  "tenant-a",
		Version:   3,
		Record: corememory.MemoryRecord{
			MemoryID: "mem_loser",
			TenantID: "tenant-a",
			Version:  3,
		},
	})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if len(projector.upserts) != 0 || len(projector.removes) != 0 {
		t.Fatalf("projector mutated = %+v (memory_dedupe_collapsed cleanup is via memory_deleted)", projector)
	}
	if len(observer.events) != 1 {
		t.Fatalf("observer events = %d, want 1", len(observer.events))
	}
	if observer.events[0].Status != "dedupe_collapsed_observed" {
		t.Fatalf("status = %q, want dedupe_collapsed_observed", observer.events[0].Status)
	}
	if observer.events[0].EventVersion != 3 {
		t.Fatalf("event_version = %d, want 3", observer.events[0].EventVersion)
	}
}

func TestOutboxVectorPublisher_PropagatesTruthSourceError(t *testing.T) {
	projector := &fakeVectorProjector{}
	records := &fakePublisherRecordStore{err: errors.New("db down")}
	observer := &fakeOutboxObserver{}
	publisher := NewOutboxVectorPublisher(records, projector, observer)

	if err := publisher.Publish(context.Background(), corememory.OutboxMessage{
		EventType: "memory_updated",
		MemoryID:  "mem_1",
		Version:   3,
		Record: corememory.MemoryRecord{
			MemoryID: "mem_1",
			TenantID: "tenant-a",
			Version:  3,
		},
	}); err == nil {
		t.Fatal("expected error")
	}
	if len(observer.events) != 1 || observer.events[0].Status != "failed" {
		t.Fatalf("observer events = %+v", observer.events)
	}
}
