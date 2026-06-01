package service

import (
	"context"

	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	pgmemory "github.com/costa92/llm-agent-memory-postgres/postgres"
)

type NullVectorCandidateSource struct{}

func NewNullVectorCandidateSource() *NullVectorCandidateSource {
	return &NullVectorCandidateSource{}
}

func (s *NullVectorCandidateSource) RecallCandidates(context.Context, authz.Scope, string, int) ([]RecallCandidate, error) {
	return nil, pgmemory.ErrNotFound
}
