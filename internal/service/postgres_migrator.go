package service

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Migrator interface {
	Migrate(ctx context.Context) error
}

type PostgresGatewayMigrator struct {
	pool *pgxpool.Pool
}

func NewPostgresGatewayMigrator(pool *pgxpool.Pool) *PostgresGatewayMigrator {
	return &PostgresGatewayMigrator{pool: pool}
}

func (m *PostgresGatewayMigrator) Migrate(ctx context.Context) error {
	if m.pool == nil {
		return fmt.Errorf("memory-gateway/service: postgres pool is required")
	}

	for _, stmt := range gatewayMigrationStatements() {
		if _, err := m.pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("memory-gateway/service: migrate gateway schema: %w", err)
		}
	}
	return nil
}

func gatewayMigrationStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS memory_gateway_session (
			tenant_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			project_id TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL,
			status TEXT NOT NULL,
			mode TEXT NOT NULL,
			closed_at TIMESTAMPTZ,
			last_heartbeat_at TIMESTAMPTZ,
			PRIMARY KEY (tenant_id, user_id, project_id, session_id)
		)`,
		`ALTER TABLE memory_gateway_session
			ADD COLUMN IF NOT EXISTS last_heartbeat_at TIMESTAMPTZ`,
		`ALTER TABLE memory_gateway_session
			ALTER COLUMN closed_at DROP NOT NULL`,
		`CREATE TABLE IF NOT EXISTS memory_gateway_scope_version (
			tenant_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			project_id TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			version BIGINT NOT NULL,
			PRIMARY KEY (tenant_id, user_id, project_id, session_id)
		)`,
	}
}
