package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	pgmemory "github.com/costa92/llm-agent-memory-postgres/postgres"
)

const sessionClosePromoteImportanceThreshold = 0.7

type SessionWorkingRecord struct {
	Record        corememory.MemoryRecord
	LatestEventID string
}

type sessionWorkingStore interface {
	corememory.RecordStore
	corememory.Promoter
	corememory.Deduper
	ListSessionWorking(ctx context.Context, tenantID, userID, projectID, sessionID string) ([]SessionWorkingRecord, error)
}

type DurableSessionCloser struct {
	store    sessionWorkingStore
	observer WorkingLifecycleObserver
}

func NewDurableSessionCloser(store sessionWorkingStore, observer WorkingLifecycleObserver) *DurableSessionCloser {
	if store == nil {
		return nil
	}
	return &DurableSessionCloser{
		store:    store,
		observer: resolveWorkingLifecycleObserver(observer),
	}
}

func (c *DurableSessionCloser) CloseSession(ctx context.Context, scope authz.Scope, mode string) error {
	if c == nil || c.store == nil {
		return nil
	}
	records, err := c.store.ListSessionWorking(ctx, scope.TenantID, scope.UserID, scope.ProjectID, scope.SessionID)
	if err != nil {
		return err
	}

	expired := 0
	droppedBeforeUse := 0
	for _, item := range records {
		switch mode {
		case "promote_and_expire":
			handled, err := c.promoteIfEligible(ctx, item)
			if err != nil {
				return err
			}
			if handled {
				continue
			}
		}

		deleted, dropped, err := c.expireWorking(ctx, item.Record)
		if err != nil {
			return err
		}
		if deleted {
			expired++
		}
		if dropped {
			droppedBeforeUse++
		}
	}

	if expired > 0 || droppedBeforeUse > 0 {
		c.observer.ObserveWorkingLifecycle(ctx, WorkingLifecycleObservation{
			TenantID:         scope.TenantID,
			Mode:             mode,
			Expired:          expired,
			DroppedBeforeUse: droppedBeforeUse,
		})
	}
	return nil
}

func (c *DurableSessionCloser) promoteIfEligible(ctx context.Context, item SessionWorkingRecord) (bool, error) {
	record := item.Record
	if !shouldPromoteOnSessionClose(record) || strings.TrimSpace(item.LatestEventID) == "" {
		return false, nil
	}

	result, err := c.store.ResolveDedupe(ctx, corememory.ResolveDedupeInput{
		TenantID:  record.TenantID,
		DedupeKey: sessionCloseDedupeKey(record),
		Candidate: record,
	})
	if err != nil {
		if isSessionCloseStale(err) {
			return true, nil
		}
		return false, err
	}
	if result.Action != corememory.DedupeNoCollision || result.WinnerID != record.MemoryID {
		return true, nil
	}

	_, err = c.store.Promote(ctx, corememory.PromoteRecordInput{
		TenantID:        record.TenantID,
		MemoryID:        record.MemoryID,
		ExpectedVersion: record.Version,
		SourceEventID:   item.LatestEventID,
		IdempotencyKey:  sessionClosePromotionKey(record, item.LatestEventID),
		Reason:          sessionClosePromoteReason(record),
	})
	if err != nil {
		if isSessionCloseStale(err) {
			return true, nil
		}
		return false, err
	}
	return true, nil
}

func (c *DurableSessionCloser) expireWorking(ctx context.Context, record corememory.MemoryRecord) (bool, bool, error) {
	_, err := c.store.DeleteRecord(ctx, corememory.DeleteRecordInput{
		TenantID:        record.TenantID,
		MemoryID:        record.MemoryID,
		ExpectedVersion: record.Version,
	})
	if err != nil {
		if isSessionCloseStale(err) {
			return false, false, nil
		}
		return false, false, err
	}
	return true, wasDroppedBeforeUse(record), nil
}

func shouldPromoteOnSessionClose(record corememory.MemoryRecord) bool {
	switch record.Source {
	case "user_saved":
		return true
	case "agent_inferred":
		return record.Importance >= sessionClosePromoteImportanceThreshold
	default:
		return false
	}
}

func sessionCloseDedupeKey(record corememory.MemoryRecord) string {
	return sessionCloseHashParts(
		record.TenantID,
		record.UserID,
		record.Category,
		record.ProjectID,
		sessionCloseNormalizedDedupeContent(record),
	)
}

func sessionCloseNormalizedDedupeContent(record corememory.MemoryRecord) string {
	if strings.TrimSpace(record.NormalizedContentHash) != "" {
		return strings.TrimSpace(record.NormalizedContentHash)
	}
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(record.Content))), " ")
}

func sessionClosePromotionKey(record corememory.MemoryRecord, eventID string) string {
	return sessionCloseHashParts(record.TenantID, record.MemoryID, eventID, "session_close_promote")
}

func sessionClosePromoteReason(record corememory.MemoryRecord) string {
	if record.Source == "user_saved" {
		return "session_close_user_saved_default"
	}
	if record.Source == "agent_inferred" && record.Importance >= sessionClosePromoteImportanceThreshold {
		return "session_close_agent_inferred_importance_threshold"
	}
	return "session_close_default_rule"
}

func sessionCloseHashParts(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func wasDroppedBeforeUse(record corememory.MemoryRecord) bool {
	return record.HitCount == 0 || record.LastAccessAt == nil
}

func isSessionCloseStale(err error) bool {
	return errors.Is(err, pgmemory.ErrVersionConflict) || errors.Is(err, pgmemory.ErrNotFound)
}

var _ SessionCloser = (*DurableSessionCloser)(nil)
