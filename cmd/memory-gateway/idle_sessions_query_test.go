package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	pgmemory "github.com/costa92/llm-agent-memory-postgres/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestBuildIdleSessionsQuery_LiveDB validates the D6 reaper's listing query:
// it returns only sessions whose working records have gone idle past the
// cutoff, and excludes recent sessions, non-working kinds, and records with no
// session_id.
func TestBuildIdleSessionsQuery_LiveDB(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_MEMORY_PG_URL")
	if dsn == "" {
		t.Skip("set LLM_AGENT_MEMORY_PG_URL to run live postgres tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	store, err := pgmemory.New(pool, pgmemory.Config{})
	if err != nil {
		t.Fatalf("pgmemory.New: %v", err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	tenant := fmt.Sprintf("rip_%d", time.Now().UnixNano())
	write := func(session, idem, kind string) string {
		rec := corememory.MemoryRecord{
			UserID:   "u",
			Kind:     kind,
			Source:   "user_saved",
			Category: "c",
			Content:  idem,
		}
		rec.ProjectID = "p"
		rec.SessionID = session
		res, err := store.WriteRecord(ctx, corememory.WriteRecordInput{
			TenantID:       tenant,
			IdempotencyKey: idem,
			RequestHash:    "h_" + idem,
			Record:         rec,
		})
		if err != nil {
			t.Fatalf("WriteRecord %s: %v", idem, err)
		}
		return res.MemoryID
	}

	idleID := write("s_idle", "rip-idle", corememory.RecordKindWorking)
	write("s_active", "rip-active", corememory.RecordKindWorking)        // too recent
	write("s_episodic", "rip-epi", corememory.RecordKindEpisodic)        // wrong kind
	write("", "rip-nosess", corememory.RecordKindWorking)                // no session_id

	// Backdate the idle session's record to 2h ago.
	old := time.Now().UTC().Add(-2 * time.Hour)
	if _, err := pool.Exec(ctx,
		`UPDATE memory_record SET created_at=$1, updated_at=$1, last_access_at=$1 WHERE memory_id=$2`,
		old, idleID,
	); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	query := buildIdleSessionsQuery(poolStorageQueryExecutor{pool: pool}, memoryRecordTableName)
	cutoff := time.Now().UTC().Add(-1 * time.Hour) // between -2h (idle) and now (active)
	scopes, err := query(ctx, cutoff)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	var got []string
	for _, s := range scopes {
		if s.TenantID == tenant {
			got = append(got, s.SessionID)
		}
	}
	if len(got) != 1 || got[0] != "s_idle" {
		t.Fatalf("idle sessions = %v, want exactly [s_idle] (active too recent; episodic wrong kind; empty session excluded)", got)
	}
}
