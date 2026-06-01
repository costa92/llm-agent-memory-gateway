package service

import (
	"sort"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
)

type recallCandidate struct {
	record        corememory.MemoryRecord
	tokenEstimate int
	score         float64
}

func buildRecallCandidates(records []corememory.MemoryRecord) []recallCandidate {
	candidates := make([]recallCandidate, 0, len(records))
	for _, record := range records {
		estimate := EstimateTokenCost(record.Content)
		candidates = append(candidates, recallCandidate{
			record:        record,
			tokenEstimate: estimate,
			score:         recallCandidateScore(record, estimate),
		})
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		if candidates[i].tokenEstimate != candidates[j].tokenEstimate {
			return candidates[i].tokenEstimate < candidates[j].tokenEstimate
		}
		return candidates[i].record.MemoryID < candidates[j].record.MemoryID
	})

	return candidates
}

func recallCandidateScore(record corememory.MemoryRecord, tokenEstimate int) float64 {
	score := record.Importance
	if score == 0 {
		score = 0.5
	}
	if record.Pinned {
		score += 2.0
	}
	if record.Source == "user_saved" {
		score += 1.0
	}
	if tokenEstimate > 0 {
		score += 1 / float64(tokenEstimate)
	}
	return score
}
