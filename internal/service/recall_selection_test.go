package service

import (
	"testing"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
)

func TestBuildRecallCandidates_SortsTowardPromptFit(t *testing.T) {
	candidates := buildRecallCandidates([]corememory.MemoryRecord{
		{
			MemoryID:   "mem_long",
			Content:    "this is a much longer user saved pinned memory that costs more tokens than the short one",
			Pinned:     true,
			Source:     "user_saved",
			Importance: 0.9,
		},
		{
			MemoryID:   "mem_short",
			Content:    "short memory",
			Pinned:     true,
			Source:     "user_saved",
			Importance: 0.8,
		},
	})

	if len(candidates) != 2 {
		t.Fatalf("len(candidates) = %d, want 2", len(candidates))
	}
	if candidates[0].record.MemoryID != "mem_short" {
		t.Fatalf("first candidate = %q, want mem_short", candidates[0].record.MemoryID)
	}
}
