package service

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	ragembed "github.com/costa92/llm-agent-rag/embed"
	ragstore "github.com/costa92/llm-agent-rag/store"
)

type fakeRAGEmbedder struct {
	text string
	vec  ragembed.Vector
}

func (f *fakeRAGEmbedder) Embed(_ context.Context, text string) (ragembed.Vector, error) {
	f.text = text
	return f.vec, nil
}

func (f *fakeRAGEmbedder) Dimension() int {
	return len(f.vec)
}

type fakeRAGStore struct {
	query ragstore.Query
	hits  []ragstore.Hit
	err   error
}

func (f *fakeRAGStore) Upsert(context.Context, []ragstore.StoredChunk) error {
	return nil
}

func (f *fakeRAGStore) Search(_ context.Context, q ragstore.Query) ([]ragstore.Hit, error) {
	f.query = q
	return f.hits, f.err
}

func (f *fakeRAGStore) List(context.Context, string, ragstore.Filter, ragstore.Filter) ([]ragstore.StoredChunk, error) {
	return nil, nil
}

func (f *fakeRAGStore) Get(context.Context, string) (ragstore.StoredChunk, error) {
	return ragstore.StoredChunk{}, nil
}

func (f *fakeRAGStore) Remove(context.Context, string) error {
	return nil
}

func (f *fakeRAGStore) RemoveByFilter(context.Context, string, ragstore.Filter) (int, error) {
	return 0, nil
}

func (f *fakeRAGStore) Stats(context.Context, string) (ragstore.Stats, error) {
	return ragstore.Stats{}, nil
}

func TestRAGStoreVectorCandidateSource_EmbedsQueryAndAppliesSecurityFilters(t *testing.T) {
	embedder := &fakeRAGEmbedder{vec: ragembed.Vector{1, 2, 3}}
	store := &fakeRAGStore{
		hits: []ragstore.Hit{
			{Chunk: ragstore.StoredChunk{ID: "mem_1"}, Score: 0.9},
			{Chunk: ragstore.StoredChunk{ID: "mem_2"}, Score: 0.7},
		},
	}
	source := NewRAGStoreVectorCandidateSource(embedder, store, "")

	candidates, err := source.RecallCandidates(context.Background(), authz.Scope{
		TenantID:  "tenant-a",
		UserID:    "user-1",
		ProjectID: "proj-x",
		SessionID: "sess-9",
	}, "export pdf", 5)
	if err != nil {
		t.Fatalf("RecallCandidates() error = %v", err)
	}
	if embedder.text != "export pdf" {
		t.Fatalf("embedded text = %q, want export pdf", embedder.text)
	}
	if store.query.Namespace != "tenant-a" {
		t.Fatalf("namespace = %q, want tenant-a", store.query.Namespace)
	}
	if got := store.query.SecurityFilters["tenant_id"]; got != "tenant-a" {
		t.Fatalf("tenant filter = %v, want tenant-a", got)
	}
	if got := store.query.SecurityFilters["session_id"]; got != "sess-9" {
		t.Fatalf("session filter = %v, want sess-9", got)
	}
	if len(candidates) != 2 || candidates[0].MemoryID != "mem_1" {
		t.Fatalf("candidates = %+v", candidates)
	}
}
