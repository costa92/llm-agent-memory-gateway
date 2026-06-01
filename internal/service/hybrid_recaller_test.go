package service

import (
	"context"
	"errors"
	"testing"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	pgmemory "github.com/costa92/llm-agent-memory-postgres/postgres"
)

type fakeCandidateSource struct {
	candidates []RecallCandidate
	err        error
}

func (f *fakeCandidateSource) RecallCandidates(context.Context, authz.Scope, string, int) ([]RecallCandidate, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.candidates, nil
}

type fakeHydrator struct {
	scope     authz.Scope
	memoryIDs []string
	records   []corememory.MemoryRecord
	err       error
}

func (f *fakeHydrator) HydrateRecords(_ context.Context, scope authz.Scope, memoryIDs []string) ([]corememory.MemoryRecord, error) {
	f.scope = scope
	f.memoryIDs = append([]string(nil), memoryIDs...)
	if f.err != nil {
		return nil, f.err
	}
	return f.records, nil
}

func TestHybridRecaller_MergesCandidateSourcesAndHydratesTopK(t *testing.T) {
	hydrator := &fakeHydrator{
		records: []corememory.MemoryRecord{
			{MemoryID: "mem_2", Content: "second"},
			{MemoryID: "mem_1", Content: "first"},
		},
	}
	recaller := NewHybridRecaller(
		hydrator,
		&fakeCandidateSource{candidates: []RecallCandidate{
			{MemoryID: "mem_1", Score: 0.7},
			{MemoryID: "mem_2", Score: 0.9},
		}},
		&fakeCandidateSource{candidates: []RecallCandidate{
			{MemoryID: "mem_1", Score: 0.8},
			{MemoryID: "mem_3", Score: 0.4},
		}},
	)

	got, err := recaller.Recall(context.Background(), authz.Scope{
		TenantID: "tenant-a",
		UserID:   "user-1",
	}, "query", 2)
	if err != nil {
		t.Fatalf("Recall() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if len(hydrator.memoryIDs) != 2 {
		t.Fatalf("hydrate ids = %v, want 2 ids", hydrator.memoryIDs)
	}
	if hydrator.memoryIDs[0] != "mem_2" || hydrator.memoryIDs[1] != "mem_1" {
		t.Fatalf("hydrate ids = %v, want [mem_2 mem_1]", hydrator.memoryIDs)
	}
}

func TestHybridRecaller_PropagatesNotFoundWhenNoCandidatesRemain(t *testing.T) {
	recaller := NewHybridRecaller(
		&fakeHydrator{},
		&fakeCandidateSource{err: pgmemory.ErrNotFound},
		&fakeCandidateSource{err: pgmemory.ErrNotFound},
	)

	_, err := recaller.Recall(context.Background(), authz.Scope{
		TenantID: "tenant-a",
		UserID:   "user-1",
	}, "missing", 5)
	if !errors.Is(err, pgmemory.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
