package service

import (
	"context"
	"errors"
	"fmt"
	"sort"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	pgmemory "github.com/costa92/llm-agent-memory-postgres/postgres"
)

type RecallBackend interface {
	Recall(ctx context.Context, scope authz.Scope, query string, topK int) ([]corememory.MemoryRecord, error)
}

type RecallCandidate struct {
	MemoryID string
	Score    float64
}

type RecallCandidateSource interface {
	RecallCandidates(ctx context.Context, scope authz.Scope, query string, topK int) ([]RecallCandidate, error)
}

type RecallRecordHydrator interface {
	HydrateRecords(ctx context.Context, scope authz.Scope, memoryIDs []string) ([]corememory.MemoryRecord, error)
}

type HybridRecaller struct {
	sources  []RecallCandidateSource
	hydrator RecallRecordHydrator
}

func NewHybridRecaller(hydrator RecallRecordHydrator, sources ...RecallCandidateSource) *HybridRecaller {
	filtered := make([]RecallCandidateSource, 0, len(sources))
	for _, source := range sources {
		if source != nil {
			filtered = append(filtered, source)
		}
	}
	return &HybridRecaller{
		sources:  filtered,
		hydrator: hydrator,
	}
}

func (r *HybridRecaller) Recall(ctx context.Context, scope authz.Scope, query string, topK int) ([]corememory.MemoryRecord, error) {
	if r.hydrator == nil {
		return nil, fmt.Errorf("memory-gateway/service: recall hydrator is required")
	}
	if len(r.sources) == 0 {
		return nil, fmt.Errorf("memory-gateway/service: at least one recall candidate source is required")
	}
	if topK <= 0 {
		topK = 8
	}

	candidates := make(map[string]float64)
	for _, source := range r.sources {
		found, err := source.RecallCandidates(ctx, scope, query, topK)
		if err != nil {
			if errors.Is(err, pgmemory.ErrNotFound) {
				continue
			}
			return nil, err
		}
		for _, candidate := range found {
			if candidate.MemoryID == "" {
				continue
			}
			if score, ok := candidates[candidate.MemoryID]; !ok || candidate.Score > score {
				candidates[candidate.MemoryID] = candidate.Score
			}
		}
	}
	if len(candidates) == 0 {
		return nil, pgmemory.ErrNotFound
	}

	memoryIDs := rankedCandidateIDs(candidates, topK)
	records, err := r.hydrator.HydrateRecords(ctx, scope, memoryIDs)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, pgmemory.ErrNotFound
	}
	return records, nil
}

func rankedCandidateIDs(scores map[string]float64, topK int) []string {
	type scoredID struct {
		memoryID string
		score    float64
	}
	ranked := make([]scoredID, 0, len(scores))
	for memoryID, score := range scores {
		ranked = append(ranked, scoredID{memoryID: memoryID, score: score})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].memoryID < ranked[j].memoryID
	})
	if topK > 0 && len(ranked) > topK {
		ranked = ranked[:topK]
	}

	out := make([]string, 0, len(ranked))
	for _, candidate := range ranked {
		out = append(out, candidate.memoryID)
	}
	return out
}
