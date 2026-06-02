package service

import (
	"context"
	"errors"
	"testing"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	pgmemory "github.com/costa92/llm-agent-memory-postgres/postgres"
)

// seedWorkingRecord writes one working-kind record in the given scope and
// returns its memory_id.
func seedWorkingRecord(t *testing.T, ctx context.Context, store *pgmemory.Store, scope authz.Scope, idem, content string) string {
	t.Helper()
	res, err := store.WriteRecord(ctx, corememory.WriteRecordInput{
		TenantID:       scope.TenantID,
		IdempotencyKey: idem,
		RequestHash:    "hash-" + idem,
		Record: corememory.MemoryRecord{
			UserID:    scope.UserID,
			ProjectID: scope.ProjectID,
			SessionID: scope.SessionID,
			Kind:      corememory.RecordKindWorking,
			Source:    "user_saved",
			Category:  "project",
			Content:   content,
		},
	})
	if err != nil {
		t.Fatalf("seed WriteRecord: %v", err)
	}
	return res.MemoryID
}

// TestNewPostgresDurableSessionCloser_LiveDB_ExpireWorking verifies the wired
// closer (adapter + DurableSessionCloser + observer over the real store)
// reclaims a session's working records and fires the lifecycle observer — the
// end-to-end "dead metric -> live" path that step 6 enables.
func TestNewPostgresDurableSessionCloser_LiveDB_ExpireWorking(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	scope := authz.Scope{TenantID: "tenant_wc", UserID: "user_wc", ProjectID: "proj_wc", SessionID: "sess_wc"}
	memoryID := seedWorkingRecord(t, ctx, store, scope, "wc-expire-1", "ephemeral working note")

	observer := &fakeWorkingLifecycleObserver{}
	closer := NewPostgresDurableSessionCloser(store, observer)
	if closer == nil {
		t.Fatal("NewPostgresDurableSessionCloser returned nil for a non-nil store")
	}

	if err := closer.CloseSession(ctx, scope, "expire_working"); err != nil {
		t.Fatalf("CloseSession(expire_working): %v", err)
	}

	// Record must be reclaimed (soft-deleted -> not visible).
	if _, err := store.GetRecord(ctx, scope.TenantID, memoryID); !errors.Is(err, pgmemory.ErrNotFound) {
		t.Fatalf("expected reclaimed record to be ErrNotFound, got err=%v", err)
	}

	// Observer must have fired with Expired >= 1 (this is what feeds
	// working_expired_total in production).
	var totalExpired int
	for _, e := range observer.events {
		totalExpired += e.Expired
	}
	if totalExpired < 1 {
		t.Fatalf("expected observer Expired >= 1, got events=%+v", observer.events)
	}
}

// TestNewPostgresDurableSessionCloser_LiveDB_PromoteAndExpire verifies that an
// eligible (user_saved) working record is promoted to episodic on close rather
// than expired.
func TestNewPostgresDurableSessionCloser_LiveDB_PromoteAndExpire(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	scope := authz.Scope{TenantID: "tenant_wp", UserID: "user_wp", ProjectID: "proj_wp", SessionID: "sess_wp"}
	memoryID := seedWorkingRecord(t, ctx, store, scope, "wp-promote-1", "durable preference")

	closer := NewPostgresDurableSessionCloser(store, &fakeWorkingLifecycleObserver{})
	if err := closer.CloseSession(ctx, scope, "promote_and_expire"); err != nil {
		t.Fatalf("CloseSession(promote_and_expire): %v", err)
	}

	// Eligible record must survive as episodic (promoted, not expired).
	rec, err := store.GetRecord(ctx, scope.TenantID, memoryID)
	if err != nil {
		t.Fatalf("expected promoted record to be visible, got err=%v", err)
	}
	if rec.Kind != corememory.RecordKindEpisodic {
		t.Fatalf("Kind = %q, want episodic (promoted)", rec.Kind)
	}
}
