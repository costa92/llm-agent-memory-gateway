package service

import (
	"context"
	"errors"
	"testing"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	ragembed "github.com/costa92/llm-agent-rag/embed"
	ragstore "github.com/costa92/llm-agent-rag/store"
)

// ragstoreHitFixture builds a minimal Hit for tests that only need a candidate
// to exist (and not be empty-ID).
func ragstoreHitFixture(memoryID string, score float64) ragstore.Hit {
	return ragstore.Hit{
		Chunk: ragstore.StoredChunk{ID: memoryID},
		Score: score,
	}
}

// fakeEmbeddingMetrics records every call made through the EmbeddingMetricsSink
// surface so the tests can assert per-call increments.
type fakeEmbeddingMetrics struct {
	requests map[string]uint64
	applied  map[string]uint64
	tokens   map[string]uint64
	cost     map[string]uint64
}

func newFakeEmbeddingMetrics() *fakeEmbeddingMetrics {
	return &fakeEmbeddingMetrics{
		requests: make(map[string]uint64),
		applied:  make(map[string]uint64),
		tokens:   make(map[string]uint64),
		cost:     make(map[string]uint64),
	}
}

func (f *fakeEmbeddingMetrics) AddEmbeddingRequest(bucket string) {
	f.requests[bucket]++
}

func (f *fakeEmbeddingMetrics) AddEmbeddingApplied(bucket string) {
	f.applied[bucket]++
}

func (f *fakeEmbeddingMetrics) AddEmbeddingTokens(bucket string, n uint64) {
	f.tokens[bucket] += n
}

func (f *fakeEmbeddingMetrics) AddEmbeddingCost(bucket string, micros uint64) {
	f.cost[bucket] += micros
}

type erroringEmbedder struct {
	calls int
	err   error
}

func (e *erroringEmbedder) Embed(context.Context, string) (ragembed.Vector, error) {
	e.calls++
	return nil, e.err
}

func (e *erroringEmbedder) Dimension() int { return 8 }

func TestVectorProjector_EmbeddingMetricsOnSuccess(t *testing.T) {
	store := &fakeProjectorStore{}
	metrics := newFakeEmbeddingMetrics()
	embedder := ragembed.NewHashEmbedder(8)
	projector := NewRAGVectorProjector(embedder, store, "")
	projector.SetEmbeddingMetrics(metrics, 10) // 10 micros per token

	if err := projector.ProjectUpsert(context.Background(), authz.Scope{TenantID: "tenant-a"}, corememory.MemoryRecord{
		MemoryID: "mem_1",
		TenantID: "tenant-a",
		Content:  "remember this short note",
	}); err != nil {
		t.Fatalf("ProjectUpsert() error = %v", err)
	}

	bucket := TenantBucket("tenant-a")
	if metrics.requests[bucket] != 1 {
		t.Fatalf("requests[%s] = %d, want 1", bucket, metrics.requests[bucket])
	}
	if metrics.applied[bucket] != 1 {
		t.Fatalf("applied[%s] = %d, want 1", bucket, metrics.applied[bucket])
	}
	// "remember this short note" -> 4 whitespace-separated tokens.
	if metrics.tokens[bucket] != 4 {
		t.Fatalf("tokens[%s] = %d, want 4", bucket, metrics.tokens[bucket])
	}
	// cost = 4 tokens * 10 micros/token.
	if metrics.cost[bucket] != 40 {
		t.Fatalf("cost[%s] = %d, want 40", bucket, metrics.cost[bucket])
	}
}

func TestVectorProjector_EmbeddingMetricsOnErrorOnlyRequestIncrements(t *testing.T) {
	store := &fakeProjectorStore{}
	metrics := newFakeEmbeddingMetrics()
	embedder := &erroringEmbedder{err: errors.New("embed boom")}
	projector := NewRAGVectorProjector(embedder, store, "")
	projector.SetEmbeddingMetrics(metrics, 10)

	err := projector.ProjectUpsert(context.Background(), authz.Scope{TenantID: "tenant-b"}, corememory.MemoryRecord{
		MemoryID: "mem_1",
		TenantID: "tenant-b",
		Content:  "anything",
	})
	if err == nil {
		t.Fatal("expected error from erroring embedder")
	}

	bucket := TenantBucket("tenant-b")
	if metrics.requests[bucket] != 1 {
		t.Fatalf("requests[%s] = %d, want 1 (always counted)", bucket, metrics.requests[bucket])
	}
	if metrics.applied[bucket] != 0 || metrics.tokens[bucket] != 0 || metrics.cost[bucket] != 0 {
		t.Fatalf("error path leaked into success counters: applied=%d tokens=%d cost=%d",
			metrics.applied[bucket], metrics.tokens[bucket], metrics.cost[bucket])
	}
}

func TestVectorProjector_EmbeddingMetricsZeroCostRate(t *testing.T) {
	store := &fakeProjectorStore{}
	metrics := newFakeEmbeddingMetrics()
	projector := NewRAGVectorProjector(ragembed.NewHashEmbedder(8), store, "")
	projector.SetEmbeddingMetrics(metrics, 0) // unwired cost rate

	if err := projector.ProjectUpsert(context.Background(), authz.Scope{TenantID: "tenant-c"}, corememory.MemoryRecord{
		MemoryID: "mem_1",
		TenantID: "tenant-c",
		Content:  "two words",
	}); err != nil {
		t.Fatalf("ProjectUpsert() error = %v", err)
	}

	bucket := TenantBucket("tenant-c")
	if metrics.tokens[bucket] != 2 {
		t.Fatalf("tokens[%s] = %d, want 2", bucket, metrics.tokens[bucket])
	}
	if metrics.cost[bucket] != 0 {
		t.Fatalf("cost[%s] = %d, want 0 (zero cost rate)", bucket, metrics.cost[bucket])
	}
}

func TestVectorProjector_NoEmbeddingMetricsSinkIsSafe(t *testing.T) {
	store := &fakeProjectorStore{}
	projector := NewRAGVectorProjector(ragembed.NewHashEmbedder(8), store, "")
	// SetEmbeddingMetrics not called — projector should still work.

	if err := projector.ProjectUpsert(context.Background(), authz.Scope{TenantID: "tenant-d"}, corememory.MemoryRecord{
		MemoryID: "mem_1",
		TenantID: "tenant-d",
		Content:  "ok",
	}); err != nil {
		t.Fatalf("ProjectUpsert() error = %v", err)
	}
}

func TestRAGStoreVectorCandidateSource_EmbeddingMetricsOnSuccess(t *testing.T) {
	embedder := &fakeRAGEmbedder{vec: ragembed.Vector{1, 2, 3}}
	// Provide at least one hit so RecallCandidates returns success.
	store := &fakeRAGStore{}
	store.hits = append(store.hits, ragstoreHitFixture("mem_1", 0.9))
	source := NewRAGStoreVectorCandidateSource(embedder, store, "")
	metrics := newFakeEmbeddingMetrics()
	source.SetEmbeddingMetrics(metrics, 25)

	_, err := source.RecallCandidates(context.Background(), authz.Scope{TenantID: "tenant-x"}, "four word recall query", 3)
	if err != nil {
		t.Fatalf("RecallCandidates() error = %v", err)
	}

	bucket := TenantBucket("tenant-x")
	if metrics.requests[bucket] != 1 {
		t.Fatalf("requests[%s] = %d, want 1", bucket, metrics.requests[bucket])
	}
	if metrics.applied[bucket] != 1 {
		t.Fatalf("applied[%s] = %d, want 1", bucket, metrics.applied[bucket])
	}
	// "four word recall query" -> 4 tokens
	if metrics.tokens[bucket] != 4 {
		t.Fatalf("tokens[%s] = %d, want 4", bucket, metrics.tokens[bucket])
	}
	if metrics.cost[bucket] != 100 {
		t.Fatalf("cost[%s] = %d, want 100 (4*25)", bucket, metrics.cost[bucket])
	}
}

func TestRAGStoreVectorCandidateSource_EmbeddingMetricsOnError(t *testing.T) {
	embedder := &erroringEmbedder{err: errors.New("embed boom")}
	source := NewRAGStoreVectorCandidateSource(embedder, &fakeRAGStore{}, "")
	metrics := newFakeEmbeddingMetrics()
	source.SetEmbeddingMetrics(metrics, 25)

	_, err := source.RecallCandidates(context.Background(), authz.Scope{TenantID: "tenant-y"}, "query", 1)
	if err == nil {
		t.Fatal("expected error from erroring embedder")
	}

	bucket := TenantBucket("tenant-y")
	if metrics.requests[bucket] != 1 {
		t.Fatalf("requests[%s] = %d, want 1", bucket, metrics.requests[bucket])
	}
	if metrics.applied[bucket] != 0 || metrics.tokens[bucket] != 0 || metrics.cost[bucket] != 0 {
		t.Fatalf("error path leaked: applied=%d tokens=%d cost=%d",
			metrics.applied[bucket], metrics.tokens[bucket], metrics.cost[bucket])
	}
}
