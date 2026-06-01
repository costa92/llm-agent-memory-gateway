package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	pgmemory "github.com/costa92/llm-agent-memory-postgres/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

type lexicalCandidateRow struct {
	MemoryID string
	Score    float64
}

type PostgresLexicalCandidateSource struct {
	pool *pgxpool.Pool
}

func NewPostgresLexicalCandidateSource(pool *pgxpool.Pool) *PostgresLexicalCandidateSource {
	return &PostgresLexicalCandidateSource{pool: pool}
}

func (r *PostgresLexicalCandidateSource) RecallCandidates(ctx context.Context, scope authz.Scope, query string, topK int) ([]RecallCandidate, error) {
	if r.pool == nil {
		return nil, fmt.Errorf("memory-gateway/service: postgres pool is required")
	}
	if topK <= 0 {
		topK = 8
	}

	queryPattern := "%"
	if trimmed := strings.TrimSpace(query); trimmed != "" {
		queryPattern = "%" + trimmed + "%"
	}

	const statement = `
SELECT
	memory_id,
	CASE
		WHEN pinned THEN 1000
		ELSE 0
	END
	+ CASE
		WHEN source = 'user_saved' THEN 100
		ELSE 0
	END
	+ importance
FROM memory_record
WHERE tenant_id = $1
  AND user_id = $2
  AND deleted = FALSE
  AND disabled = FALSE
  AND ($3 = '' OR project_id = $3 OR project_id IS NULL)
  AND ($4 = '' OR session_id = $4 OR session_id IS NULL)
  AND (
	$5 = '%%'
	OR content ILIKE $5
	OR category ILIKE $5
	OR EXISTS (
		SELECT 1
		FROM jsonb_array_elements_text(tags) tag
		WHERE tag ILIKE $5
	)
  )
ORDER BY 2 DESC, updated_at DESC, memory_id ASC
LIMIT $6`

	rows, err := r.pool.Query(ctx, statement, scope.TenantID, scope.UserID, scope.ProjectID, scope.SessionID, queryPattern, topK)
	if err != nil {
		return nil, fmt.Errorf("memory-gateway/service: recall candidate query: %w", err)
	}
	defer rows.Close()

	var out []RecallCandidate
	for rows.Next() {
		var row lexicalCandidateRow
		if err := rows.Scan(&row.MemoryID, &row.Score); err != nil {
			return nil, fmt.Errorf("memory-gateway/service: scan recall candidate row: %w", err)
		}
		out = append(out, RecallCandidate{
			MemoryID: row.MemoryID,
			Score:    row.Score,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory-gateway/service: iterate recall candidate rows: %w", err)
	}
	if len(out) == 0 {
		return nil, pgmemory.ErrNotFound
	}
	return out, nil
}

type PostgresRecordHydrator struct {
	store corememory.RecordStore
}

func NewPostgresRecordHydrator(store corememory.RecordStore) *PostgresRecordHydrator {
	return &PostgresRecordHydrator{store: store}
}

func (h *PostgresRecordHydrator) HydrateRecords(ctx context.Context, scope authz.Scope, memoryIDs []string) ([]corememory.MemoryRecord, error) {
	if h.store == nil {
		return nil, fmt.Errorf("memory-gateway/service: record hydrator store is required")
	}

	records := make([]corememory.MemoryRecord, 0, len(memoryIDs))
	for _, memoryID := range memoryIDs {
		record, err := h.store.GetRecord(ctx, scope.TenantID, memoryID)
		if err != nil {
			if err == pgmemory.ErrNotFound {
				continue
			}
			return nil, err
		}
		if !recordBelongsToScope(record, scope) {
			continue
		}
		records = append(records, record)
	}
	if len(records) == 0 {
		return nil, pgmemory.ErrNotFound
	}
	return records, nil
}

func recordBelongsToScope(record corememory.MemoryRecord, scope authz.Scope) bool {
	if record.TenantID != scope.TenantID || record.UserID != scope.UserID {
		return false
	}
	if scope.ProjectID != "" && record.ProjectID != "" && record.ProjectID != scope.ProjectID {
		return false
	}
	if scope.SessionID != "" && record.SessionID != "" && record.SessionID != scope.SessionID {
		return false
	}
	return true
}

type recordRow struct {
	MemoryID   string
	TenantID   string
	UserID     string
	ProjectID  string
	SessionID  string
	Kind       string
	Source     string
	Category   string
	Content    string
	Tags       []string
	Importance float64
	Pinned     bool
	Disabled   bool
	Version    int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
