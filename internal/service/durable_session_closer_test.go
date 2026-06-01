package service

import (
	"context"
	"errors"
	"testing"
	"time"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	pgmemory "github.com/costa92/llm-agent-memory-postgres/postgres"
)

type fakeSessionWorkingStore struct {
	records      []SessionWorkingRecord
	listErr      error
	listCalls    int
	listScope    authz.Scope
	deleteCalls  []corememory.DeleteRecordInput
	deleteErr    error
	promoteCalls []corememory.PromoteRecordInput
	promoteErr   error
	dedupeCalls  []corememory.ResolveDedupeInput
	dedupeErr    error
	dedupeResult corememory.ResolveDedupeResult
}

func (f *fakeSessionWorkingStore) GetRecord(context.Context, string, string) (corememory.MemoryRecord, error) {
	return corememory.MemoryRecord{}, nil
}

func (f *fakeSessionWorkingStore) WriteRecord(context.Context, corememory.WriteRecordInput) (corememory.WriteRecordResult, error) {
	return corememory.WriteRecordResult{}, nil
}

func (f *fakeSessionWorkingStore) PatchRecord(context.Context, corememory.PatchRecordInput) (corememory.PatchRecordResult, error) {
	return corememory.PatchRecordResult{}, nil
}

func (f *fakeSessionWorkingStore) DeleteRecord(_ context.Context, in corememory.DeleteRecordInput) (corememory.DeleteRecordResult, error) {
	f.deleteCalls = append(f.deleteCalls, in)
	if f.deleteErr != nil {
		return corememory.DeleteRecordResult{}, f.deleteErr
	}
	return corememory.DeleteRecordResult{
		MemoryID: in.MemoryID,
		Version:  in.ExpectedVersion + 1,
		Record: corememory.MemoryRecord{
			MemoryID: in.MemoryID,
			Deleted:  true,
			Version:  in.ExpectedVersion + 1,
		},
	}, nil
}

func (f *fakeSessionWorkingStore) PinRecord(context.Context, corememory.PinRecordInput) (corememory.PinRecordResult, error) {
	return corememory.PinRecordResult{}, nil
}

func (f *fakeSessionWorkingStore) DisableRecord(context.Context, corememory.DisableRecordInput) (corememory.DisableRecordResult, error) {
	return corememory.DisableRecordResult{}, nil
}

func (f *fakeSessionWorkingStore) Promote(_ context.Context, in corememory.PromoteRecordInput) (corememory.PromoteRecordResult, error) {
	f.promoteCalls = append(f.promoteCalls, in)
	if f.promoteErr != nil {
		return corememory.PromoteRecordResult{}, f.promoteErr
	}
	return corememory.PromoteRecordResult{
		MemoryID: in.MemoryID,
		Version:  in.ExpectedVersion + 1,
		Created:  true,
		Record: corememory.MemoryRecord{
			MemoryID:                in.MemoryID,
			Kind:                    corememory.RecordKindEpisodic,
			Version:                 in.ExpectedVersion + 1,
			ConsolidatedFromEventID: in.SourceEventID,
		},
	}, nil
}

func (f *fakeSessionWorkingStore) ResolveDedupe(_ context.Context, in corememory.ResolveDedupeInput) (corememory.ResolveDedupeResult, error) {
	f.dedupeCalls = append(f.dedupeCalls, in)
	if f.dedupeErr != nil {
		return corememory.ResolveDedupeResult{}, f.dedupeErr
	}
	return f.dedupeResult, nil
}

func (f *fakeSessionWorkingStore) ListSessionWorking(_ context.Context, tenantID, userID, projectID, sessionID string) ([]SessionWorkingRecord, error) {
	f.listCalls++
	f.listScope = authz.Scope{
		TenantID:  tenantID,
		UserID:    userID,
		ProjectID: projectID,
		SessionID: sessionID,
	}
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]SessionWorkingRecord, len(f.records))
	copy(out, f.records)
	return out, nil
}

type fakeWorkingLifecycleObserver struct {
	events []WorkingLifecycleObservation
}

func (f *fakeWorkingLifecycleObserver) ObserveWorkingLifecycle(_ context.Context, obs WorkingLifecycleObservation) {
	f.events = append(f.events, obs)
}

func TestDurableSessionCloser_ExpireWorkingDeletesSessionRecordsAndObservesLifecycle(t *testing.T) {
	accessedAt := time.Now().UTC().Add(-time.Minute)
	store := &fakeSessionWorkingStore{
		records: []SessionWorkingRecord{
			{
				Record: corememory.MemoryRecord{
					MemoryID:  "mem_unused",
					TenantID:  "tenant-a",
					UserID:    "user-a",
					ProjectID: "project-a",
					SessionID: "session-a",
					Kind:      corememory.RecordKindWorking,
					Version:   3,
				},
				LatestEventID: "evt_unused",
			},
			{
				Record: corememory.MemoryRecord{
					MemoryID:     "mem_used",
					TenantID:     "tenant-a",
					UserID:       "user-a",
					ProjectID:    "project-a",
					SessionID:    "session-a",
					Kind:         corememory.RecordKindWorking,
					Version:      7,
					LastAccessAt: &accessedAt,
					HitCount:     2,
				},
				LatestEventID: "evt_used",
			},
		},
	}
	observer := &fakeWorkingLifecycleObserver{}
	closer := NewDurableSessionCloser(store, observer)

	err := closer.CloseSession(context.Background(), authz.Scope{
		TenantID:  "tenant-a",
		UserID:    "user-a",
		ProjectID: "project-a",
		SessionID: "session-a",
	}, "expire_working")
	if err != nil {
		t.Fatalf("CloseSession() error = %v", err)
	}
	if store.listCalls != 1 {
		t.Fatalf("ListSessionWorking calls = %d, want 1", store.listCalls)
	}
	if len(store.deleteCalls) != 2 {
		t.Fatalf("DeleteRecord calls = %d, want 2", len(store.deleteCalls))
	}
	if len(store.promoteCalls) != 0 || len(store.dedupeCalls) != 0 {
		t.Fatalf("unexpected promotion calls: promote=%d dedupe=%d", len(store.promoteCalls), len(store.dedupeCalls))
	}
	if got := store.deleteCalls[0].MemoryID; got != "mem_unused" {
		t.Fatalf("first deleted memory_id = %q, want mem_unused", got)
	}
	if got := store.deleteCalls[1].MemoryID; got != "mem_used" {
		t.Fatalf("second deleted memory_id = %q, want mem_used", got)
	}
	if len(observer.events) != 1 {
		t.Fatalf("observer events = %d, want 1", len(observer.events))
	}
	if got := observer.events[0].Expired; got != 2 {
		t.Fatalf("Expired = %d, want 2", got)
	}
	if got := observer.events[0].DroppedBeforeUse; got != 1 {
		t.Fatalf("DroppedBeforeUse = %d, want 1", got)
	}
}

func TestDurableSessionCloser_PromoteAndExpirePromotesEligibleAndDeletesRemainder(t *testing.T) {
	store := &fakeSessionWorkingStore{
		records: []SessionWorkingRecord{
			{
				Record: corememory.MemoryRecord{
					MemoryID:              "mem_promote",
					TenantID:              "tenant-a",
					UserID:                "user-a",
					ProjectID:             "project-a",
					SessionID:             "session-a",
					Kind:                  corememory.RecordKindWorking,
					Source:                "user_saved",
					Category:              "preference",
					NormalizedContentHash: "hash-promote",
					Version:               4,
				},
				LatestEventID: "evt_promote",
			},
			{
				Record: corememory.MemoryRecord{
					MemoryID:              "mem_expire",
					TenantID:              "tenant-a",
					UserID:                "user-a",
					ProjectID:             "project-a",
					SessionID:             "session-a",
					Kind:                  corememory.RecordKindWorking,
					Source:                "agent_inferred",
					Category:              "preference",
					NormalizedContentHash: "hash-expire",
					Importance:            0.4,
					Version:               6,
				},
				LatestEventID: "evt_expire",
			},
		},
		dedupeResult: corememory.ResolveDedupeResult{
			WinnerID: "mem_promote",
			Action:   corememory.DedupeNoCollision,
		},
	}
	observer := &fakeWorkingLifecycleObserver{}
	closer := NewDurableSessionCloser(store, observer)

	err := closer.CloseSession(context.Background(), authz.Scope{
		TenantID:  "tenant-a",
		UserID:    "user-a",
		ProjectID: "project-a",
		SessionID: "session-a",
	}, "promote_and_expire")
	if err != nil {
		t.Fatalf("CloseSession() error = %v", err)
	}
	if len(store.dedupeCalls) != 1 {
		t.Fatalf("ResolveDedupe calls = %d, want 1", len(store.dedupeCalls))
	}
	if len(store.promoteCalls) != 1 {
		t.Fatalf("Promote calls = %d, want 1", len(store.promoteCalls))
	}
	if got := store.promoteCalls[0].MemoryID; got != "mem_promote" {
		t.Fatalf("promoted memory_id = %q, want mem_promote", got)
	}
	if got := store.promoteCalls[0].SourceEventID; got != "evt_promote" {
		t.Fatalf("SourceEventID = %q, want evt_promote", got)
	}
	if len(store.deleteCalls) != 1 {
		t.Fatalf("DeleteRecord calls = %d, want 1", len(store.deleteCalls))
	}
	if got := store.deleteCalls[0].MemoryID; got != "mem_expire" {
		t.Fatalf("deleted memory_id = %q, want mem_expire", got)
	}
	if len(observer.events) != 1 {
		t.Fatalf("observer events = %d, want 1", len(observer.events))
	}
	if got := observer.events[0].Expired; got != 1 {
		t.Fatalf("Expired = %d, want 1", got)
	}
	if got := observer.events[0].DroppedBeforeUse; got != 1 {
		t.Fatalf("DroppedBeforeUse = %d, want 1", got)
	}
}

func TestDurableSessionCloser_IgnoresStaleMutations(t *testing.T) {
	store := &fakeSessionWorkingStore{
		records: []SessionWorkingRecord{
			{
				Record: corememory.MemoryRecord{
					MemoryID:              "mem_1",
					TenantID:              "tenant-a",
					UserID:                "user-a",
					SessionID:             "session-a",
					Kind:                  corememory.RecordKindWorking,
					Source:                "user_saved",
					Category:              "preference",
					NormalizedContentHash: "hash-1",
					Version:               2,
				},
				LatestEventID: "evt_1",
			},
		},
		dedupeResult: corememory.ResolveDedupeResult{
			WinnerID: "mem_1",
			Action:   corememory.DedupeNoCollision,
		},
		promoteErr: pgmemory.ErrVersionConflict,
		deleteErr:  pgmemory.ErrNotFound,
	}
	observer := &fakeWorkingLifecycleObserver{}
	closer := NewDurableSessionCloser(store, observer)

	if err := closer.CloseSession(context.Background(), authz.Scope{
		TenantID:  "tenant-a",
		UserID:    "user-a",
		SessionID: "session-a",
	}, "promote_and_expire"); err != nil {
		t.Fatalf("CloseSession() error = %v, want nil stale handling", err)
	}
	if len(observer.events) != 0 {
		t.Fatalf("observer events = %d, want 0", len(observer.events))
	}
}

func TestDurableSessionCloser_PropagatesUnexpectedListError(t *testing.T) {
	wantErr := errors.New("boom")
	closer := NewDurableSessionCloser(&fakeSessionWorkingStore{listErr: wantErr}, nil)

	err := closer.CloseSession(context.Background(), authz.Scope{
		TenantID:  "tenant-a",
		UserID:    "user-a",
		SessionID: "session-a",
	}, "expire_working")
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}
