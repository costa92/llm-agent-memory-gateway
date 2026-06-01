package service

import (
	"context"
	"fmt"

	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresScopeVersionStore struct {
	pool *pgxpool.Pool
}

func NewPostgresScopeVersionStore(pool *pgxpool.Pool) *PostgresScopeVersionStore {
	return &PostgresScopeVersionStore{pool: pool}
}

func (s *PostgresScopeVersionStore) CurrentScopeVersion(ctx context.Context, scope authz.Scope) (int64, error) {
	if s.pool == nil {
		return 0, fmt.Errorf("memory-gateway/service: postgres pool is required")
	}

	var version int64
	err := s.pool.QueryRow(ctx, `
SELECT version
FROM memory_gateway_scope_version
WHERE tenant_id = $1 AND user_id = $2 AND project_id = $3 AND session_id = $4`,
		scope.TenantID, scope.UserID, scope.ProjectID, scope.SessionID,
	).Scan(&version)
	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, nil
		}
		return 0, fmt.Errorf("memory-gateway/service: load scope version: %w", err)
	}
	return version, nil
}

func (s *PostgresScopeVersionStore) BumpScopeVersion(ctx context.Context, scope authz.Scope) (int64, error) {
	if s.pool == nil {
		return 0, fmt.Errorf("memory-gateway/service: postgres pool is required")
	}

	var version int64
	err := s.pool.QueryRow(ctx, `
INSERT INTO memory_gateway_scope_version (
	tenant_id, user_id, project_id, session_id, version
) VALUES ($1, $2, $3, $4, 1)
ON CONFLICT (tenant_id, user_id, project_id, session_id)
DO UPDATE SET version = memory_gateway_scope_version.version + 1
RETURNING version`,
		scope.TenantID, scope.UserID, scope.ProjectID, scope.SessionID,
	).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("memory-gateway/service: bump scope version: %w", err)
	}
	return version, nil
}
