package service

import (
	"context"
	"errors"
	"testing"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	pgmemory "github.com/costa92/llm-agent-memory-postgres/postgres"
)

type fakeRecordStore struct {
	records map[string]corememory.MemoryRecord
	err     error
}

func (f *fakeRecordStore) GetRecord(context.Context, string, string) (corememory.MemoryRecord, error) {
	return corememory.MemoryRecord{}, nil
}

func (f *fakeRecordStore) WriteRecord(context.Context, corememory.WriteRecordInput) (corememory.WriteRecordResult, error) {
	return corememory.WriteRecordResult{}, nil
}

func (f *fakeRecordStore) PatchRecord(context.Context, corememory.PatchRecordInput) (corememory.PatchRecordResult, error) {
	return corememory.PatchRecordResult{}, nil
}

func (f *fakeRecordStore) DeleteRecord(context.Context, corememory.DeleteRecordInput) (corememory.DeleteRecordResult, error) {
	return corememory.DeleteRecordResult{}, nil
}

func (f *fakeRecordStore) PinRecord(context.Context, corememory.PinRecordInput) (corememory.PinRecordResult, error) {
	return corememory.PinRecordResult{}, nil
}

func (f *fakeRecordStore) DisableRecord(context.Context, corememory.DisableRecordInput) (corememory.DisableRecordResult, error) {
	return corememory.DisableRecordResult{}, nil
}

type fakeHydrationStore struct {
	records map[string]corememory.MemoryRecord
	err     error
}

func (f *fakeHydrationStore) GetRecord(_ context.Context, _ string, memoryID string) (corememory.MemoryRecord, error) {
	if f.err != nil {
		return corememory.MemoryRecord{}, f.err
	}
	record, ok := f.records[memoryID]
	if !ok {
		return corememory.MemoryRecord{}, pgmemory.ErrNotFound
	}
	return record, nil
}

func (f *fakeHydrationStore) WriteRecord(context.Context, corememory.WriteRecordInput) (corememory.WriteRecordResult, error) {
	return corememory.WriteRecordResult{}, nil
}

func (f *fakeHydrationStore) PatchRecord(context.Context, corememory.PatchRecordInput) (corememory.PatchRecordResult, error) {
	return corememory.PatchRecordResult{}, nil
}

func (f *fakeHydrationStore) DeleteRecord(context.Context, corememory.DeleteRecordInput) (corememory.DeleteRecordResult, error) {
	return corememory.DeleteRecordResult{}, nil
}

func (f *fakeHydrationStore) PinRecord(context.Context, corememory.PinRecordInput) (corememory.PinRecordResult, error) {
	return corememory.PinRecordResult{}, nil
}

func (f *fakeHydrationStore) DisableRecord(context.Context, corememory.DisableRecordInput) (corememory.DisableRecordResult, error) {
	return corememory.DisableRecordResult{}, nil
}

func TestPostgresRecordHydrator_EnforcesDBSideScopeVisibility(t *testing.T) {
	hydrator := NewPostgresRecordHydrator(&fakeHydrationStore{
		records: map[string]corememory.MemoryRecord{
			"mem_1": {MemoryID: "mem_1", TenantID: "tenant-a", UserID: "user-1", ProjectID: "proj-a"},
			"mem_2": {MemoryID: "mem_2", TenantID: "tenant-a", UserID: "user-1", ProjectID: "proj-b"},
		},
	})

	records, err := hydrator.HydrateRecords(context.Background(), authz.Scope{
		TenantID:  "tenant-a",
		UserID:    "user-1",
		ProjectID: "proj-a",
	}, []string{"mem_1", "mem_2"})
	if err != nil {
		t.Fatalf("HydrateRecords() error = %v", err)
	}
	if len(records) != 1 || records[0].MemoryID != "mem_1" {
		t.Fatalf("records = %+v, want only mem_1", records)
	}
}

func TestPostgresRecordHydrator_PropagatesNotFoundWhenAllCandidatesFiltered(t *testing.T) {
	hydrator := NewPostgresRecordHydrator(&fakeHydrationStore{
		records: map[string]corememory.MemoryRecord{
			"mem_2": {MemoryID: "mem_2", TenantID: "tenant-a", UserID: "user-1", ProjectID: "proj-b"},
		},
	})

	_, err := hydrator.HydrateRecords(context.Background(), authz.Scope{
		TenantID:  "tenant-a",
		UserID:    "user-1",
		ProjectID: "proj-a",
	}, []string{"mem_2"})
	if !errors.Is(err, pgmemory.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
