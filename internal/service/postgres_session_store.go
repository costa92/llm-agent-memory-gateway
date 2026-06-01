package service

import (
	"context"
	"fmt"
	"time"

	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresSessionStateStore struct {
	pool *pgxpool.Pool
}

func NewPostgresSessionStateStore(pool *pgxpool.Pool) *PostgresSessionStateStore {
	return &PostgresSessionStateStore{pool: pool}
}

func (s *PostgresSessionStateStore) LoadSessionState(ctx context.Context, scope authz.Scope) (SessionState, bool, error) {
	if s.pool == nil {
		return SessionState{}, false, fmt.Errorf("memory-gateway/service: postgres pool is required")
	}

	var (
		state           SessionState
		lastHeartbeatAt pgtype.Timestamptz
	)
	err := s.pool.QueryRow(ctx, `
SELECT tenant_id, user_id, COALESCE(project_id, ''), session_id, status, mode, closed_at, last_heartbeat_at
FROM memory_gateway_session
WHERE tenant_id = $1 AND user_id = $2 AND project_id = $3 AND session_id = $4`,
		scope.TenantID, scope.UserID, scope.ProjectID, scope.SessionID,
	).Scan(
		&state.TenantID,
		&state.UserID,
		&state.ProjectID,
		&state.SessionID,
		&state.Status,
		&state.Mode,
		&state.ClosedAt,
		&lastHeartbeatAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return SessionState{}, false, nil
		}
		return SessionState{}, false, fmt.Errorf("memory-gateway/service: load session state: %w", err)
	}
	if lastHeartbeatAt.Valid {
		state.LastHeartbeatAt = lastHeartbeatAt.Time
	}
	return state, true, nil
}

func (s *PostgresSessionStateStore) SaveClosedSession(ctx context.Context, scope authz.Scope, mode string, now time.Time) (SessionState, error) {
	if s.pool == nil {
		return SessionState{}, fmt.Errorf("memory-gateway/service: postgres pool is required")
	}

	var (
		state           SessionState
		lastHeartbeatAt pgtype.Timestamptz
	)
	err := s.pool.QueryRow(ctx, `
INSERT INTO memory_gateway_session (
	tenant_id, user_id, project_id, session_id, status, mode, closed_at, last_heartbeat_at
) VALUES ($1, $2, $3, $4, 'closed', $5, $6, NULL)
ON CONFLICT (tenant_id, user_id, project_id, session_id)
DO UPDATE SET
	status = CASE
		WHEN memory_gateway_session.status = 'closed' THEN memory_gateway_session.status
		ELSE 'closed'
	END,
	mode = CASE
		WHEN memory_gateway_session.status = 'closed' THEN memory_gateway_session.mode
		ELSE EXCLUDED.mode
	END,
	closed_at = CASE
		WHEN memory_gateway_session.status = 'closed' THEN memory_gateway_session.closed_at
		ELSE EXCLUDED.closed_at
	END,
	last_heartbeat_at = memory_gateway_session.last_heartbeat_at
RETURNING tenant_id, user_id, COALESCE(project_id, ''), session_id, status, mode, closed_at, last_heartbeat_at`,
		scope.TenantID, scope.UserID, scope.ProjectID, scope.SessionID, mode, now,
	).Scan(
		&state.TenantID,
		&state.UserID,
		&state.ProjectID,
		&state.SessionID,
		&state.Status,
		&state.Mode,
		&state.ClosedAt,
		&lastHeartbeatAt,
	)
	if err != nil {
		return SessionState{}, fmt.Errorf("memory-gateway/service: save closed session: %w", err)
	}
	if lastHeartbeatAt.Valid {
		state.LastHeartbeatAt = lastHeartbeatAt.Time
	}
	return state, nil
}

func (s *PostgresSessionStateStore) SaveHeartbeat(ctx context.Context, scope authz.Scope, now time.Time) (SessionState, error) {
	if s.pool == nil {
		return SessionState{}, fmt.Errorf("memory-gateway/service: postgres pool is required")
	}

	var (
		state           SessionState
		lastHeartbeatAt pgtype.Timestamptz
	)
	err := s.pool.QueryRow(ctx, `
INSERT INTO memory_gateway_session (
	tenant_id, user_id, project_id, session_id, status, mode, closed_at, last_heartbeat_at
) VALUES ($1, $2, $3, $4, 'active', '', NULL, $5)
ON CONFLICT (tenant_id, user_id, project_id, session_id)
DO UPDATE SET
	status = CASE
		WHEN memory_gateway_session.status = 'closed' THEN memory_gateway_session.status
		ELSE 'active'
	END,
	mode = CASE
		WHEN memory_gateway_session.status = 'closed' THEN memory_gateway_session.mode
		ELSE ''
	END,
	closed_at = CASE
		WHEN memory_gateway_session.status = 'closed' THEN memory_gateway_session.closed_at
		ELSE NULL
	END
	,last_heartbeat_at = CASE
		WHEN memory_gateway_session.status = 'closed' THEN memory_gateway_session.last_heartbeat_at
		ELSE EXCLUDED.last_heartbeat_at
	END
RETURNING tenant_id, user_id, COALESCE(project_id, ''), session_id, status, mode, closed_at, last_heartbeat_at`,
		scope.TenantID, scope.UserID, scope.ProjectID, scope.SessionID, now,
	).Scan(
		&state.TenantID,
		&state.UserID,
		&state.ProjectID,
		&state.SessionID,
		&state.Status,
		&state.Mode,
		&state.ClosedAt,
		&lastHeartbeatAt,
	)
	if err != nil {
		return SessionState{}, fmt.Errorf("memory-gateway/service: save heartbeat: %w", err)
	}
	if lastHeartbeatAt.Valid {
		state.LastHeartbeatAt = lastHeartbeatAt.Time
	}
	return state, nil
}
