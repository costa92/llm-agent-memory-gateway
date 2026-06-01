package service

import (
	"context"
	"errors"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	pgmemory "github.com/costa92/llm-agent-memory-postgres/postgres"
)

type OutboxVectorPublisher struct {
	records   corememory.RecordStore
	projector VectorProjector
	observer  OutboxProjectionObserver
}

func NewOutboxVectorPublisher(records corememory.RecordStore, projector VectorProjector, observer OutboxProjectionObserver) *OutboxVectorPublisher {
	return &OutboxVectorPublisher{
		records:   records,
		projector: projector,
		observer:  resolveOutboxProjectionObserver(observer),
	}
}

func (p *OutboxVectorPublisher) Publish(ctx context.Context, msg corememory.OutboxMessage) error {
	if p == nil || p.projector == nil || p.records == nil {
		return nil
	}

	switch msg.EventType {
	case "memory_created", "memory_updated", "memory_pinned", "memory_unpinned", "memory_enabled":
		current, ok, err := p.currentRecord(ctx, msg)
		if err != nil {
			if errors.Is(err, pgmemory.ErrNotFound) {
				p.observe(ctx, msg, "stale", 0, "truth_source_missing")
				return nil
			}
			p.observe(ctx, msg, "failed", 0, "truth_source_error")
			return err
		}
		if !ok || current.Version != msg.Version {
			p.observe(ctx, msg, "stale", current.Version, "version_mismatch")
			return nil
		}
		if err := p.projector.ProjectUpsert(ctx, scopeFromRecord(current), current); err != nil {
			p.observe(ctx, msg, "failed", current.Version, "project_upsert_error")
			return err
		}
		p.observe(ctx, msg, "projected", current.Version, "upsert")
		return nil
	case "memory_promoted":
		// Promotion is a state-only event: the underlying memory_record
		// already exists and was projected at memory_created time. No
		// vector mutation is needed; the observation lets metrics keep
		// the per-event counter shape consistent.
		p.observe(ctx, msg, "promoted_noop", msg.Version, "")
		return nil
	case "memory_dedupe_collapsed":
		// Dedupe collapse marks one memory as the "loser" of a merge.
		// The actual loser cleanup (vector removal) is emitted as a
		// matching memory_deleted event in the same M8a transaction,
		// so this case only records that the collapse happened.
		p.observe(ctx, msg, "dedupe_collapsed_observed", msg.Version, "")
		return nil
	case "memory_disabled", "memory_deleted":
		current, ok, err := p.currentRecord(ctx, msg)
		if err != nil && !errors.Is(err, pgmemory.ErrNotFound) {
			p.observe(ctx, msg, "failed", 0, "truth_source_error")
			return err
		}
		if ok && current.Version != msg.Version {
			p.observe(ctx, msg, "stale", current.Version, "version_mismatch")
			return nil
		}
		if err := p.projector.ProjectRemove(ctx, scopeFromRecord(msg.Record), msg.MemoryID); err != nil {
			p.observe(ctx, msg, "failed", current.Version, "project_remove_error")
			return err
		}
		p.observe(ctx, msg, "projected", current.Version, "remove")
		return nil
	default:
		p.observe(ctx, msg, "ignored", 0, "unsupported_event")
		return nil
	}
}

func (p *OutboxVectorPublisher) currentRecord(ctx context.Context, msg corememory.OutboxMessage) (corememory.MemoryRecord, bool, error) {
	record, err := p.records.GetRecord(ctx, msg.TenantID, msg.MemoryID)
	if err != nil {
		if errors.Is(err, pgmemory.ErrNotFound) {
			return corememory.MemoryRecord{}, false, err
		}
		return corememory.MemoryRecord{}, false, err
	}
	return record, true, nil
}

func scopeFromRecord(record corememory.MemoryRecord) authz.Scope {
	return authz.Scope{
		TenantID:  record.TenantID,
		UserID:    record.UserID,
		ProjectID: record.ProjectID,
		SessionID: record.SessionID,
	}
}

func (p *OutboxVectorPublisher) observe(ctx context.Context, msg corememory.OutboxMessage, status string, currentVersion int64, reason string) {
	if p == nil || p.observer == nil {
		return
	}
	p.observer.ObserveProjection(ctx, OutboxProjectionObservation{
		Status:         status,
		EventType:      msg.EventType,
		TenantID:       msg.TenantID,
		MemoryID:       msg.MemoryID,
		EventVersion:   msg.Version,
		CurrentVersion: currentVersion,
		Reason:         reason,
	})
}
