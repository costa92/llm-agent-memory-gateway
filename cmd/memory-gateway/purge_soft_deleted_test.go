package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	pgmemory "github.com/costa92/llm-agent-memory-postgres/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestBuildPurgeSoftDeleted_LiveDB validates the D4 GC's purge: it physically
// removes only rows that are soft-deleted AND older than the cutoff, leaving
// recently-deleted rows and live rows intact.
func TestBuildPurgeSoftDeleted_LiveDB(t *testing.T) {
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

	tenant := fmt.Sprintf("gc_%d", time.Now().UnixNano())
	write := func(idem string) string {
		res, err := store.WriteRecord(ctx, corememory.WriteRecordInput{
			TenantID:       tenant,
			IdempotencyKey: idem,
			RequestHash:    "h_" + idem,
			Record:         corememory.MemoryRecord{UserID: "u", Kind: corememory.RecordKindEpisodic, Source: "user_saved", Category: "c", Content: idem},
		})
		if err != nil {
			t.Fatalf("WriteRecord %s: %v", idem, err)
		}
		return res.MemoryID
	}

	oldDeleted := write("gc-old-deleted")    // soft-deleted long ago -> purged
	recentDeleted := write("gc-recent-deleted") // soft-deleted just now -> kept
	live := write("gc-live")                 // not deleted -> kept

	// Soft-delete two of them with distinct deleted_at.
	if _, err := pool.Exec(ctx, `UPDATE memory_record SET deleted=TRUE, deleted_at=$1 WHERE memory_id=$2`,
		time.Now().UTC().Add(-40*24*time.Hour), oldDeleted); err != nil {
		t.Fatalf("backdate old: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memory_record SET deleted=TRUE, deleted_at=$1 WHERE memory_id=$2`,
		time.Now().UTC(), recentDeleted); err != nil {
		t.Fatalf("delete recent: %v", err)
	}

	purge := buildPurgeSoftDeleted(pool, memoryRecordTableName)
	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour) // 30-day retention
	n, err := purge(ctx, cutoff)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n < 1 {
		t.Fatalf("purged %d rows, want >= 1 (the 40-day-old soft-deleted row)", n)
	}

	exists := func(id string) bool {
		var present bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memory_record WHERE memory_id=$1)`, id).Scan(&present); err != nil {
			t.Fatalf("exists %s: %v", id, err)
		}
		return present
	}

	if exists(oldDeleted) {
		t.Fatal("old soft-deleted row should have been physically removed")
	}
	if !exists(recentDeleted) {
		t.Fatal("recently soft-deleted row must be kept (within retention)")
	}
	if !exists(live) {
		t.Fatal("live (non-deleted) row must never be touched")
	}

	// GetRecord on the purged row is gone; ErrNotFound either way is fine.
	if _, err := store.GetRecord(ctx, tenant, oldDeleted); !errors.Is(err, pgmemory.ErrNotFound) {
		t.Fatalf("purged record GetRecord err = %v, want ErrNotFound", err)
	}
}
