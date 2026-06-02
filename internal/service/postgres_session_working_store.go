package service

import (
	"context"

	pgmemory "github.com/costa92/llm-agent-memory-postgres/postgres"
)

// postgresSessionWorkingStore adapts *pgmemory.Store to the sessionWorkingStore
// interface that DurableSessionCloser requires.
//
// The embedded *pgmemory.Store already satisfies corememory.RecordStore,
// corememory.Promoter, and corememory.Deduper directly, so those are promoted
// as-is. Only ListSessionWorking needs adapting: the store returns its own
// pgmemory.SessionWorkingRecord, while the closer consumes the gateway's
// service.SessionWorkingRecord — structurally identical, but distinct types.
// The method below shadows the embedded one and performs the conversion.
type postgresSessionWorkingStore struct {
	*pgmemory.Store
}

// newPostgresSessionWorkingStore wraps a non-nil store. It returns nil when
// store is nil so callers can pass the result straight to
// NewDurableSessionCloser (which treats a nil store as "no closer").
func newPostgresSessionWorkingStore(store *pgmemory.Store) *postgresSessionWorkingStore {
	if store == nil {
		return nil
	}
	return &postgresSessionWorkingStore{Store: store}
}

func (s *postgresSessionWorkingStore) ListSessionWorking(ctx context.Context, tenantID, userID, projectID, sessionID string) ([]SessionWorkingRecord, error) {
	rows, err := s.Store.ListSessionWorking(ctx, tenantID, userID, projectID, sessionID)
	if err != nil {
		return nil, err
	}
	return convertSessionWorkingRecords(rows), nil
}

// convertSessionWorkingRecords maps the postgres row type to the gateway type
// 1:1. A nil input yields a nil slice (preserving "no rows" exactly).
func convertSessionWorkingRecords(rows []pgmemory.SessionWorkingRecord) []SessionWorkingRecord {
	if rows == nil {
		return nil
	}
	out := make([]SessionWorkingRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, SessionWorkingRecord{
			Record:        r.Record,
			LatestEventID: r.LatestEventID,
		})
	}
	return out
}

var _ sessionWorkingStore = (*postgresSessionWorkingStore)(nil)
