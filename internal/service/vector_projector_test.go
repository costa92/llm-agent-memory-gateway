package service

import (
	"context"
	"testing"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	ragembed "github.com/costa92/llm-agent-rag/embed"
	ragstore "github.com/costa92/llm-agent-rag/store"
)

type fakeProjectorStore struct {
	upserts []ragstore.StoredChunk
	removed []string
}

func (f *fakeProjectorStore) Upsert(_ context.Context, chunks []ragstore.StoredChunk) error {
	f.upserts = append(f.upserts, chunks...)
	return nil
}

func (f *fakeProjectorStore) Search(context.Context, ragstore.Query) ([]ragstore.Hit, error) {
	return nil, nil
}

func (f *fakeProjectorStore) List(context.Context, string, ragstore.Filter, ragstore.Filter) ([]ragstore.StoredChunk, error) {
	return nil, nil
}

func (f *fakeProjectorStore) Get(context.Context, string) (ragstore.StoredChunk, error) {
	return ragstore.StoredChunk{}, nil
}

func (f *fakeProjectorStore) Remove(_ context.Context, id string) error {
	f.removed = append(f.removed, id)
	return nil
}

func (f *fakeProjectorStore) RemoveByFilter(context.Context, string, ragstore.Filter) (int, error) {
	return 0, nil
}

func (f *fakeProjectorStore) Stats(context.Context, string) (ragstore.Stats, error) {
	return ragstore.Stats{}, nil
}

func TestRAGVectorProjector_ProjectUpsertBuildsChunk(t *testing.T) {
	store := &fakeProjectorStore{}
	projector := NewRAGVectorProjector(ragembed.NewHashEmbedder(8), store, "")

	err := projector.ProjectUpsert(context.Background(), authz.Scope{
		TenantID: "tenant-a",
	}, corememory.MemoryRecord{
		MemoryID:  "mem_1",
		TenantID:  "tenant-a",
		UserID:    "user-1",
		ProjectID: "proj-x",
		SessionID: "sess-9",
		Kind:      "semantic",
		Source:    "user_saved",
		Category:  "project",
		Content:   "remember this",
		Version:   4,
	})
	if err != nil {
		t.Fatalf("ProjectUpsert() error = %v", err)
	}
	if len(store.upserts) != 1 {
		t.Fatalf("upserts = %d, want 1", len(store.upserts))
	}
	chunk := store.upserts[0]
	if chunk.ID != "mem_1" || chunk.Namespace != "tenant-a" {
		t.Fatalf("chunk = %+v", chunk)
	}
	if got := chunk.Metadata["version"]; got != int64(4) {
		t.Fatalf("metadata[version] = %v, want 4", got)
	}
}

func TestRAGVectorProjector_ProjectRemoveDeletesChunk(t *testing.T) {
	store := &fakeProjectorStore{}
	projector := NewRAGVectorProjector(ragembed.NewHashEmbedder(8), store, "")

	if err := projector.ProjectRemove(context.Background(), authz.Scope{}, "mem_1"); err != nil {
		t.Fatalf("ProjectRemove() error = %v", err)
	}
	if len(store.removed) != 1 || store.removed[0] != "mem_1" {
		t.Fatalf("removed = %v, want [mem_1]", store.removed)
	}
}
