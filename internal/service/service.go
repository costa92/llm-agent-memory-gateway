package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	"github.com/costa92/llm-agent-memory-gateway/internal/httpapi"
	pgmemory "github.com/costa92/llm-agent-memory-postgres/postgres"
)

var ErrBackendRequired = errors.New("memory-gateway/service: backend is required")

type DurableBackend interface {
	corememory.RecordStore
}

type SessionCloser interface {
	CloseSession(ctx context.Context, scope authz.Scope, mode string) error
}

type TraceEmitter interface {
	Emit(ctx context.Context, stage string, fields map[string]any)
}

type Config struct {
	ReadOnly            bool
	SessionStateStore   SessionStateStore
	ScopeVersionStore   ScopeVersionStore
	SessionIdleTTL      time.Duration
	RecallObserver      RecallObserver
	RecallCacheObserver RecallCacheObserver
	IdempotencyStore    corememory.IdempotencyStore
}

type Interface interface {
	RecallUnified(ctx context.Context, authScope authz.Scope, req httpapi.RecallUnifiedRequest) (httpapi.RecallUnifiedResponse, error)
	WriteMemory(ctx context.Context, authScope authz.Scope, req httpapi.WriteMemoryRequest) (httpapi.WriteMemoryResponse, error)
	PatchMemory(ctx context.Context, authScope authz.Scope, memoryID string, req httpapi.PatchMemoryRequest) (httpapi.PatchMemoryResponse, error)
	PinMemory(ctx context.Context, authScope authz.Scope, memoryID string, req httpapi.PinMemoryRequest) (httpapi.PinMemoryResponse, error)
	UnpinMemory(ctx context.Context, authScope authz.Scope, memoryID string, req httpapi.PinMemoryRequest) (httpapi.PinMemoryResponse, error)
	DisableMemory(ctx context.Context, authScope authz.Scope, memoryID string, req httpapi.DisableMemoryRequest) (httpapi.DisableMemoryResponse, error)
	EnableMemory(ctx context.Context, authScope authz.Scope, memoryID string, req httpapi.DisableMemoryRequest) (httpapi.DisableMemoryResponse, error)
	DeleteMemory(ctx context.Context, authScope authz.Scope, memoryID string, req httpapi.DeleteMemoryRequest) (httpapi.DeleteMemoryResponse, error)
	CloseSession(ctx context.Context, authScope authz.Scope, sessionID string, req httpapi.SessionCloseRequest) (httpapi.SessionCloseResponse, error)
	HeartbeatSession(ctx context.Context, authScope authz.Scope, sessionID string, req httpapi.SessionHeartbeatRequest) (httpapi.SessionHeartbeatResponse, error)
	GetMemoryItem(ctx context.Context, authScope authz.Scope, memoryID string) (httpapi.GetMemoryItemResponse, error)
}

type Service struct {
	backend             DurableBackend
	recaller            RecallBackend
	sessionCloser       SessionCloser
	traceEmitter        TraceEmitter
	readOnly            bool
	recallCache         *recallCache
	sessionRegistry     *sessionRegistry
	scopeVersionStore   ScopeVersionStore
	sessionIdleTTL      time.Duration
	recallObserver      RecallObserver
	recallCacheObserver RecallCacheObserver
	idempotencyStore    corememory.IdempotencyStore
}

var _ Interface = (*Service)(nil)

type nopTraceEmitter struct{}

func (nopTraceEmitter) Emit(context.Context, string, map[string]any) {}

func New(backend DurableBackend, recaller RecallBackend, sessionCloser SessionCloser, traceEmitter TraceEmitter, cfg Config) (*Service, error) {
	if backend == nil {
		return nil, ErrBackendRequired
	}
	if traceEmitter == nil {
		traceEmitter = nopTraceEmitter{}
	}
	return &Service{
		backend:             backend,
		recaller:            recaller,
		sessionCloser:       sessionCloser,
		traceEmitter:        traceEmitter,
		readOnly:            cfg.ReadOnly,
		recallCache:         newRecallCache(),
		sessionRegistry:     newSessionRegistry(cfg.SessionStateStore),
		scopeVersionStore:   resolveScopeVersionStore(cfg.ScopeVersionStore),
		sessionIdleTTL:      resolveSessionIdleTTL(cfg.SessionIdleTTL),
		recallObserver:      resolveRecallObserver(cfg.RecallObserver),
		recallCacheObserver: resolveRecallCacheObserver(cfg.RecallCacheObserver),
		idempotencyStore:    cfg.IdempotencyStore,
	}, nil
}

func (s *Service) RecallUnified(ctx context.Context, authScope authz.Scope, req httpapi.RecallUnifiedRequest) (httpapi.RecallUnifiedResponse, error) {
	if strings.TrimSpace(req.Query) == "" {
		return httpapi.RecallUnifiedResponse{}, httpapi.ErrBadRequest("query is required", nil)
	}
	if req.TopK < 0 {
		return httpapi.RecallUnifiedResponse{}, httpapi.ErrBadRequest("top_k must be non-negative", nil)
	}
	if req.TopK == 0 {
		req.TopK = 8
	}
	if req.TopK > 50 {
		return httpapi.RecallUnifiedResponse{}, httpapi.ErrBadRequest("top_k must be <= 50", nil)
	}
	if s.recaller == nil {
		return httpapi.RecallUnifiedResponse{}, httpapi.ErrNotFound("recall backend is not configured", nil)
	}

	scope := mergeScope(authScope, req.Scope)
	if state, ok, err := s.sessionRegistry.Get(ctx, scope); err != nil {
		return httpapi.RecallUnifiedResponse{}, translateBackendError(err)
	} else if err := s.validateSessionState(state, ok, time.Now().UTC()); err != nil {
		return httpapi.RecallUnifiedResponse{}, err
	}
	consistencyLevel := consistencyLevelOrDefault(req.ConsistencyLevel)
	cacheKey := buildRecallCacheKey(scope, req)
	scopeVersion, err := s.scopeVersionStore.CurrentScopeVersion(ctx, scope)
	if err != nil {
		return httpapi.RecallUnifiedResponse{}, translateBackendError(err)
	}
	if consistencyLevel != "strong" {
		if cached, ok, staleServed := s.recallCache.Lookup(cacheKey, consistencyLevel, req.AllowStaleCache, time.Now().UTC(), scopeVersion); ok {
			if consistencyLevel == "bounded" {
				valid, err := s.validateBoundedCachedResponse(ctx, scope, cached)
				if err != nil {
					return httpapi.RecallUnifiedResponse{}, translateBackendError(err)
				}
				if !valid {
					goto recallOrigin
				}
			}
			if cached.Trace == nil {
				cached.Trace = &httpapi.RecallTraceResponse{}
			}
			cacheLevel := "l1_hit"
			if consistencyLevel == "bounded" {
				cacheLevel = "l2_hit"
			}
			cached.Trace.CacheLevel = cacheLevel
			cached.Trace.ConsistencyLevel = consistencyLevel
			cached.Trace.StaleServed = staleServed
			s.recallObserver.ObserveRecall(ctx, RecallObservation{
				ConsistencyLevel: consistencyLevel,
				CacheLevel:       cacheLevel,
				StaleServed:      staleServed,
				TenantID:         scope.TenantID,
				// Returned/Selected intentionally zero on cache-hit paths —
				// no fresh recall was performed.
			})
			return cached, nil
		}
	}

recallOrigin:
	records, err := s.recaller.Recall(ctx, scope, req.Query, req.TopK)
	if err != nil {
		if errors.Is(err, pgmemory.ErrNotFound) {
			return httpapi.RecallUnifiedResponse{Hits: []httpapi.RecallHitResponse{}}, nil
		}
		return httpapi.RecallUnifiedResponse{}, translateBackendError(err)
	}

	s.traceEmitter.Emit(ctx, "recalled", map[string]any{
		"tenant_id": scope.TenantID,
		"user_id":   scope.UserID,
		"count":     len(records),
	})

	response := httpapi.RecallUnifiedResponse{
		Hits: make([]httpapi.RecallHitResponse, 0, len(records)),
	}
	totalEstimate := 0
	for _, candidate := range buildRecallCandidates(records) {
		record := candidate.record
		estimate := candidate.tokenEstimate
		if req.MemoryTokenBudget > 0 && totalEstimate+estimate > req.MemoryTokenBudget {
			s.traceEmitter.Emit(ctx, "dropped", map[string]any{
				"memory_id":           record.MemoryID,
				"token_cost_estimate": estimate,
				"memory_token_budget": req.MemoryTokenBudget,
			})
			continue
		}

		totalEstimate += estimate
		response.Hits = append(response.Hits, httpapi.RecallHitResponse{
			MemoryID: record.MemoryID,
			Kind:     record.Kind,
			Score:    candidate.score,
			Version:  record.Version,
			Content:  record.Content,
			Tags:     record.Tags,
			Source:   record.Source,
			Category: record.Category,
			Pinned:   record.Pinned,
			Disabled: record.Disabled,
			Metadata: httpapi.RecallHitMetadata{
				MatchedBy:         "long_term_unified",
				TokenCostEstimate: estimate,
			},
		})
		s.traceEmitter.Emit(ctx, "selected", map[string]any{
			"memory_id":           record.MemoryID,
			"token_cost_estimate": estimate,
		})
	}

	// Mark the returned records accessed (hit_count / last_access_at) when the
	// backend supports it. Only the origin path reaches here — cache hits
	// return above — so a cached response never re-marks access. Best-effort
	// per backend capability; a marker error fails the recall like any other
	// backend error.
	if marker, ok := s.backend.(corememory.AccessMarker); ok && len(response.Hits) > 0 {
		ids := make([]string, 0, len(response.Hits))
		for _, hit := range response.Hits {
			ids = append(ids, hit.MemoryID)
		}
		if err := marker.MarkAccess(ctx, corememory.MarkAccessInput{
			TenantID:   scope.TenantID,
			MemoryIDs:  ids,
			AccessedAt: time.Now().UTC(),
		}); err != nil {
			return httpapi.RecallUnifiedResponse{}, translateBackendError(err)
		}
	}

	// Observe after post-budget filtering so Returned/Selected carry the
	// pre- and post-filter counts. Cache-hit paths above leave these zero,
	// which the metrics observer treats as a no-op.
	s.recallObserver.ObserveRecall(ctx, RecallObservation{
		ConsistencyLevel: consistencyLevel,
		CacheLevel:       "origin",
		StaleServed:      false,
		TenantID:         scope.TenantID,
		Returned:         len(records),
		Selected:         len(response.Hits),
	})

	s.traceEmitter.Emit(ctx, "promote_decided", map[string]any{
		"session_id": scope.SessionID,
		"mode":       "deferred",
	})

	if req.Debug {
		response.Trace = &httpapi.RecallTraceResponse{
			CacheLevel:            "origin",
			ConsistencyLevel:      consistencyLevel,
			StaleServed:           false,
			MemoryTokenBudget:     req.MemoryTokenBudget,
			ReturnedTokenEstimate: totalEstimate,
		}
	}
	if response.Trace == nil {
		response.Trace = &httpapi.RecallTraceResponse{
			CacheLevel:       "origin",
			ConsistencyLevel: consistencyLevel,
			StaleServed:      false,
		}
	}
	if consistencyLevel != "strong" {
		s.recallCache.Set(cacheKey, response, time.Now().UTC(), scopeVersion)
		s.recallCacheObserver.ObserveRecallCache(ctx, RecallCacheObservation{
			Action: "fill",
			Scope:  scope,
		})
	}

	return response, nil
}

func (s *Service) WriteMemory(ctx context.Context, authScope authz.Scope, req httpapi.WriteMemoryRequest) (httpapi.WriteMemoryResponse, error) {
	if err := s.ensureWritable(); err != nil {
		return httpapi.WriteMemoryResponse{}, err
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return httpapi.WriteMemoryResponse{}, httpapi.ErrBadRequest("idempotency_key is required", nil)
	}
	if strings.TrimSpace(req.Record.Content) == "" {
		return httpapi.WriteMemoryResponse{}, httpapi.ErrBadRequest("record.content is required", nil)
	}
	kind, err := corememory.NormalizeRecordKind(req.Record.Kind)
	if err != nil {
		return httpapi.WriteMemoryResponse{}, httpapi.ErrBadRequest("record.kind must be one of working, episodic, semantic", nil)
	}

	scope := mergeScope(authScope, req.Scope)
	writeInput := corememory.WriteRecordInput{
		TenantID:       scope.TenantID,
		IdempotencyKey: req.IdempotencyKey,
		RequestHash:    hashWriteRequest(scope, req.Record),
		Record: corememory.MemoryRecord{
			TenantID:   scope.TenantID,
			UserID:     scope.UserID,
			ProjectID:  scope.ProjectID,
			SessionID:  scope.SessionID,
			Kind:       kind,
			Source:     req.Record.Source,
			Category:   req.Record.Category,
			Content:    req.Record.Content,
			Tags:       req.Record.Tags,
			Importance: req.Record.Importance,
			Pinned:     req.Record.Pinned,
		},
	}

	result, err := s.backend.WriteRecord(ctx, writeInput)
	if err != nil {
		return httpapi.WriteMemoryResponse{}, translateBackendError(err)
	}
	s.invalidateScopeState(ctx, scope)

	return httpapi.WriteMemoryResponse{
		Memory: httpapi.WriteMemoryResult{
			MemoryID: result.MemoryID,
			Version:  result.Version,
			Status:   "saved",
		},
	}, nil
}

func (s *Service) PatchMemory(ctx context.Context, authScope authz.Scope, memoryID string, req httpapi.PatchMemoryRequest) (httpapi.PatchMemoryResponse, error) {
	if err := s.ensureWritable(); err != nil {
		return httpapi.PatchMemoryResponse{}, err
	}
	if req.ExpectedVersion <= 0 {
		return httpapi.PatchMemoryResponse{}, httpapi.ErrBadRequest("expected_version is required", nil)
	}

	scope := mergeScope(authScope, req.Scope)

	if strings.TrimSpace(req.IdempotencyKey) != "" && s.idempotencyStore != nil {
		requestHash := hashPatchRequest(scope, memoryID, req)
		entry, err := s.idempotencyStore.LoadIdempotency(ctx, scope.TenantID, req.IdempotencyKey)
		if err != nil && !errors.Is(err, pgmemory.ErrNotFound) {
			return httpapi.PatchMemoryResponse{}, translateBackendError(err)
		}
		if err == nil {
			// Key exists — replay or conflict.
			if entry.RequestHash != requestHash {
				return httpapi.PatchMemoryResponse{}, httpapi.ErrIdempotencyConflict("idempotency_key conflicts with an existing payload", nil)
			}
			return httpapi.PatchMemoryResponse{
				MemoryID: entry.Response.MemoryID,
				Version:  entry.Response.Version,
			}, nil
		}
		// Key not found — run the patch then save the snapshot.
		result, err := s.doPatchRecord(ctx, scope, memoryID, req)
		if err != nil {
			return httpapi.PatchMemoryResponse{}, err
		}
		snap := corememory.IdempotencyEntry{
			TenantID:       scope.TenantID,
			IdempotencyKey: req.IdempotencyKey,
			RequestHash:    requestHash,
			MemoryID:       result.MemoryID,
			Response: corememory.WriteRecordResult{
				MemoryID: result.MemoryID,
				Version:  result.Version,
				Record:   result.Record,
			},
			CreatedAt: time.Now().UTC(),
		}
		if saveErr := s.idempotencyStore.SaveIdempotency(ctx, snap); saveErr != nil {
			// Best-effort: the patch already committed; log via translateBackendError
			// shape but don't fail the caller — they got the patched result.
			_ = saveErr
		}
		s.invalidateScopeState(ctx, scope)
		return httpapi.PatchMemoryResponse{MemoryID: result.MemoryID, Version: result.Version}, nil
	}

	result, err := s.doPatchRecord(ctx, scope, memoryID, req)
	if err != nil {
		return httpapi.PatchMemoryResponse{}, err
	}
	s.invalidateScopeState(ctx, scope)
	return httpapi.PatchMemoryResponse{MemoryID: result.MemoryID, Version: result.Version}, nil
}

func (s *Service) doPatchRecord(ctx context.Context, scope authz.Scope, memoryID string, req httpapi.PatchMemoryRequest) (corememory.PatchRecordResult, error) {
	input := corememory.PatchRecordInput{
		TenantID:        scope.TenantID,
		MemoryID:        memoryID,
		ExpectedVersion: req.ExpectedVersion,
	}
	if req.Patch.Content != nil {
		input.Content = *req.Patch.Content
	}
	if req.Patch.Category != nil {
		input.Category = *req.Patch.Category
	}
	if req.Patch.Tags != nil {
		input.Tags = *req.Patch.Tags
	}
	if req.Patch.Importance != nil {
		input.Importance = *req.Patch.Importance
	}
	result, err := s.backend.PatchRecord(ctx, input)
	if err != nil {
		return corememory.PatchRecordResult{}, translateBackendError(err)
	}
	return result, nil
}

func (s *Service) PinMemory(ctx context.Context, authScope authz.Scope, memoryID string, req httpapi.PinMemoryRequest) (httpapi.PinMemoryResponse, error) {
	if err := s.ensureWritable(); err != nil {
		return httpapi.PinMemoryResponse{}, err
	}
	if req.ExpectedVersion <= 0 {
		return httpapi.PinMemoryResponse{}, httpapi.ErrBadRequest("expected_version is required", nil)
	}

	scope := mergeScope(authScope, req.Scope)
	if record, ok := s.terminalStateShortCircuit(ctx, scope, memoryID, req.ExpectedVersion, func(r corememory.MemoryRecord) bool {
		return r.Pinned
	}); ok {
		return httpapi.PinMemoryResponse{
			MemoryID: record.MemoryID,
			Version:  record.Version,
			Pinned:   true,
		}, nil
	}
	result, err := s.backend.PinRecord(ctx, corememory.PinRecordInput{
		TenantID:        scope.TenantID,
		MemoryID:        memoryID,
		ExpectedVersion: req.ExpectedVersion,
		Pinned:          true,
	})
	if err != nil {
		return httpapi.PinMemoryResponse{}, translateBackendError(err)
	}
	s.invalidateScopeState(ctx, scope)
	return httpapi.PinMemoryResponse{
		MemoryID: result.MemoryID,
		Version:  result.Version,
		Pinned:   result.Record.Pinned,
	}, nil
}

// terminalStateShortCircuit reads the current record and reports whether a
// pin/unpin/enable mutation can be replayed as a no-op success. It returns
// (record, true) only when the record is already in the desired terminal state
// AND the caller's expectedVersion is not ahead of the stored version — a
// retry after the first mutation already succeeded. On any GetRecord error
// (including ErrNotFound for deleted/disabled records) it returns ok=false so
// the caller falls through to the backend mutation, which surfaces the
// canonical error and enforces optimistic concurrency.
func (s *Service) terminalStateShortCircuit(ctx context.Context, scope authz.Scope, memoryID string, expectedVersion int64, desired func(corememory.MemoryRecord) bool) (corememory.MemoryRecord, bool) {
	record, err := s.backend.GetRecord(ctx, scope.TenantID, memoryID)
	if err != nil {
		return corememory.MemoryRecord{}, false
	}
	if desired(record) && expectedVersion <= record.Version {
		return record, true
	}
	return corememory.MemoryRecord{}, false
}

// terminalHiddenStateShortCircuit mirrors terminalStateShortCircuit but reads
// through GetRecordIncludingHidden so disable/delete replays can reconcile
// against records the visibility filter would hide (Deleted/Disabled). It
// returns (record, true) only when the record is already in the desired
// terminal state AND the caller's expectedVersion is not ahead of the stored
// version. On any read error (including ErrNotFound for truly-absent records)
// it returns ok=false so the caller falls through to the backend mutation.
func (s *Service) terminalHiddenStateShortCircuit(ctx context.Context, scope authz.Scope, memoryID string, expectedVersion int64, desired func(corememory.MemoryRecord) bool) (corememory.MemoryRecord, bool) {
	record, err := s.backend.GetRecordIncludingHidden(ctx, scope.TenantID, memoryID)
	if err != nil {
		return corememory.MemoryRecord{}, false
	}
	if desired(record) && expectedVersion <= record.Version {
		return record, true
	}
	return corememory.MemoryRecord{}, false
}

func (s *Service) UnpinMemory(ctx context.Context, authScope authz.Scope, memoryID string, req httpapi.PinMemoryRequest) (httpapi.PinMemoryResponse, error) {
	if err := s.ensureWritable(); err != nil {
		return httpapi.PinMemoryResponse{}, err
	}
	if req.ExpectedVersion <= 0 {
		return httpapi.PinMemoryResponse{}, httpapi.ErrBadRequest("expected_version is required", nil)
	}

	scope := mergeScope(authScope, req.Scope)
	if record, ok := s.terminalStateShortCircuit(ctx, scope, memoryID, req.ExpectedVersion, func(r corememory.MemoryRecord) bool {
		return !r.Pinned
	}); ok {
		return httpapi.PinMemoryResponse{
			MemoryID: record.MemoryID,
			Version:  record.Version,
			Pinned:   false,
		}, nil
	}
	result, err := s.backend.PinRecord(ctx, corememory.PinRecordInput{
		TenantID:        scope.TenantID,
		MemoryID:        memoryID,
		ExpectedVersion: req.ExpectedVersion,
		Pinned:          false,
	})
	if err != nil {
		return httpapi.PinMemoryResponse{}, translateBackendError(err)
	}
	s.invalidateScopeState(ctx, scope)
	return httpapi.PinMemoryResponse{
		MemoryID: result.MemoryID,
		Version:  result.Version,
		Pinned:   result.Record.Pinned,
	}, nil
}

func (s *Service) DisableMemory(ctx context.Context, authScope authz.Scope, memoryID string, req httpapi.DisableMemoryRequest) (httpapi.DisableMemoryResponse, error) {
	if err := s.ensureWritable(); err != nil {
		return httpapi.DisableMemoryResponse{}, err
	}
	if req.ExpectedVersion <= 0 {
		return httpapi.DisableMemoryResponse{}, httpapi.ErrBadRequest("expected_version is required", nil)
	}

	scope := mergeScope(authScope, req.Scope)
	if record, ok := s.terminalHiddenStateShortCircuit(ctx, scope, memoryID, req.ExpectedVersion, func(r corememory.MemoryRecord) bool {
		return r.Disabled && !r.Deleted
	}); ok {
		return httpapi.DisableMemoryResponse{
			MemoryID: record.MemoryID,
			Version:  record.Version,
			Disabled: true,
		}, nil
	}
	result, err := s.backend.DisableRecord(ctx, corememory.DisableRecordInput{
		TenantID:        scope.TenantID,
		MemoryID:        memoryID,
		ExpectedVersion: req.ExpectedVersion,
		Disabled:        true,
	})
	if err != nil {
		return httpapi.DisableMemoryResponse{}, translateBackendError(err)
	}
	s.invalidateScopeState(ctx, scope)
	return httpapi.DisableMemoryResponse{
		MemoryID: result.MemoryID,
		Version:  result.Version,
		Disabled: result.Record.Disabled,
	}, nil
}

func (s *Service) EnableMemory(ctx context.Context, authScope authz.Scope, memoryID string, req httpapi.DisableMemoryRequest) (httpapi.DisableMemoryResponse, error) {
	if err := s.ensureWritable(); err != nil {
		return httpapi.DisableMemoryResponse{}, err
	}
	if req.ExpectedVersion <= 0 {
		return httpapi.DisableMemoryResponse{}, httpapi.ErrBadRequest("expected_version is required", nil)
	}

	scope := mergeScope(authScope, req.Scope)
	if record, ok := s.terminalStateShortCircuit(ctx, scope, memoryID, req.ExpectedVersion, func(r corememory.MemoryRecord) bool {
		return !r.Disabled
	}); ok {
		return httpapi.DisableMemoryResponse{
			MemoryID: record.MemoryID,
			Version:  record.Version,
			Disabled: false,
		}, nil
	}
	result, err := s.backend.DisableRecord(ctx, corememory.DisableRecordInput{
		TenantID:        scope.TenantID,
		MemoryID:        memoryID,
		ExpectedVersion: req.ExpectedVersion,
		Disabled:        false,
	})
	if err != nil {
		return httpapi.DisableMemoryResponse{}, translateBackendError(err)
	}
	s.invalidateScopeState(ctx, scope)
	return httpapi.DisableMemoryResponse{
		MemoryID: result.MemoryID,
		Version:  result.Version,
		Disabled: result.Record.Disabled,
	}, nil
}

func (s *Service) DeleteMemory(ctx context.Context, authScope authz.Scope, memoryID string, req httpapi.DeleteMemoryRequest) (httpapi.DeleteMemoryResponse, error) {
	if err := s.ensureWritable(); err != nil {
		return httpapi.DeleteMemoryResponse{}, err
	}
	if req.ExpectedVersion <= 0 {
		return httpapi.DeleteMemoryResponse{}, httpapi.ErrBadRequest("expected_version is required", nil)
	}

	scope := mergeScope(authScope, req.Scope)
	if record, ok := s.terminalHiddenStateShortCircuit(ctx, scope, memoryID, req.ExpectedVersion, func(r corememory.MemoryRecord) bool {
		return r.Deleted
	}); ok {
		return httpapi.DeleteMemoryResponse{
			MemoryID: record.MemoryID,
			Deleted:  true,
			Version:  record.Version,
		}, nil
	}
	result, err := s.backend.DeleteRecord(ctx, corememory.DeleteRecordInput{
		TenantID:        scope.TenantID,
		MemoryID:        memoryID,
		ExpectedVersion: req.ExpectedVersion,
	})
	if err != nil {
		return httpapi.DeleteMemoryResponse{}, translateBackendError(err)
	}
	s.invalidateScopeState(ctx, scope)
	return httpapi.DeleteMemoryResponse{
		MemoryID: result.MemoryID,
		Deleted:  result.Record.Deleted,
		Version:  result.Version,
	}, nil
}

func (s *Service) CloseSession(ctx context.Context, authScope authz.Scope, sessionID string, req httpapi.SessionCloseRequest) (httpapi.SessionCloseResponse, error) {
	if err := s.ensureWritable(); err != nil {
		return httpapi.SessionCloseResponse{}, err
	}

	scope := mergeScope(authScope, req.Scope)
	if authScope.SessionID != "" {
		scope.SessionID = authScope.SessionID
	} else if sessionID != "" {
		scope.SessionID = sessionID
	}
	if scope.SessionID == "" {
		return httpapi.SessionCloseResponse{}, httpapi.ErrBadRequest("session_id is required", nil)
	}
	if req.Mode == "" {
		req.Mode = "expire_working"
	}
	if req.Mode != "expire_working" && req.Mode != "promote_and_expire" {
		return httpapi.SessionCloseResponse{}, httpapi.ErrBadRequest("mode must be expire_working or promote_and_expire", nil)
	}
	existing, ok, err := s.sessionRegistry.Get(ctx, scope)
	if err != nil {
		return httpapi.SessionCloseResponse{}, translateBackendError(err)
	}
	if ok && existing.Status == "closed" {
		// Idempotent replay: session already closed. Skip the closer (avoids
		// double promote/expire and a duplicate promote_decided trace) and
		// reconcile the caller with the current closed state.
		return httpapi.SessionCloseResponse{
			SessionID: existing.SessionID,
			Status:    existing.Status,
		}, nil
	}
	if s.sessionCloser != nil {
		if err := s.sessionCloser.CloseSession(ctx, scope, req.Mode); err != nil {
			return httpapi.SessionCloseResponse{}, translateBackendError(err)
		}
	}
	state, err := s.sessionRegistry.Close(ctx, scope, req.Mode, time.Now().UTC())
	if err != nil {
		return httpapi.SessionCloseResponse{}, translateBackendError(err)
	}
	s.invalidateScopeState(ctx, scope)
	s.traceEmitter.Emit(ctx, "promote_decided", map[string]any{
		"tenant_id":  scope.TenantID,
		"user_id":    scope.UserID,
		"project_id": scope.ProjectID,
		"session_id": scope.SessionID,
		"mode":       req.Mode,
	})
	return httpapi.SessionCloseResponse{
		SessionID: state.SessionID,
		Status:    state.Status,
	}, nil
}

func (s *Service) HeartbeatSession(ctx context.Context, authScope authz.Scope, sessionID string, req httpapi.SessionHeartbeatRequest) (httpapi.SessionHeartbeatResponse, error) {
	if err := s.ensureWritable(); err != nil {
		return httpapi.SessionHeartbeatResponse{}, err
	}

	scope := mergeScope(authScope, req.Scope)
	if authScope.SessionID != "" {
		scope.SessionID = authScope.SessionID
	} else if sessionID != "" {
		scope.SessionID = sessionID
	}
	if scope.SessionID == "" {
		return httpapi.SessionHeartbeatResponse{}, httpapi.ErrBadRequest("session_id is required", nil)
	}

	state, ok, err := s.sessionRegistry.Get(ctx, scope)
	if err != nil {
		return httpapi.SessionHeartbeatResponse{}, translateBackendError(err)
	}
	if err := s.validateSessionState(state, ok, time.Now().UTC()); err != nil {
		return httpapi.SessionHeartbeatResponse{}, err
	}

	state, err = s.sessionRegistry.Heartbeat(ctx, scope, time.Now().UTC())
	if err != nil {
		return httpapi.SessionHeartbeatResponse{}, translateBackendError(err)
	}
	s.traceEmitter.Emit(ctx, "promote_decided", map[string]any{
		"tenant_id":  scope.TenantID,
		"user_id":    scope.UserID,
		"project_id": scope.ProjectID,
		"session_id": scope.SessionID,
		"mode":       "heartbeat",
	})
	return httpapi.SessionHeartbeatResponse{
		SessionID: state.SessionID,
		Status:    state.Status,
	}, nil
}

func (s *Service) GetMemoryItem(ctx context.Context, authScope authz.Scope, memoryID string) (httpapi.GetMemoryItemResponse, error) {
	record, err := s.backend.GetRecord(ctx, authScope.TenantID, memoryID)
	if err != nil {
		return httpapi.GetMemoryItemResponse{}, translateBackendError(err)
	}
	return httpapi.GetMemoryItemResponse{
		MemoryID:   record.MemoryID,
		Kind:       record.Kind,
		Version:    record.Version,
		Content:    record.Content,
		Tags:       record.Tags,
		Source:     record.Source,
		Category:   record.Category,
		Importance: record.Importance,
		Pinned:     record.Pinned,
		Disabled:   record.Disabled,
	}, nil
}

func (s *Service) ensureWritable() error {
	if !s.readOnly {
		return nil
	}
	return httpapi.ErrReadOnlyMode("memory gateway is temporarily read-only", nil)
}

func mergeScope(authScope authz.Scope, claimed httpapi.ScopePayload) authz.Scope {
	return authz.MergeAuthoritativeScope(authScope, authz.Scope{
		TenantID:  claimed.TenantID,
		UserID:    claimed.UserID,
		ProjectID: claimed.ProjectID,
		SessionID: claimed.SessionID,
	})
}

func hashWriteRequest(scope authz.Scope, record httpapi.WriteRecordPayload) string {
	payload := struct {
		Scope  authz.Scope                `json:"scope"`
		Record httpapi.WriteRecordPayload `json:"record"`
	}{
		Scope:  scope,
		Record: record,
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func hashPatchRequest(scope authz.Scope, memoryID string, req httpapi.PatchMemoryRequest) string {
	payload := struct {
		Scope           authz.Scope               `json:"scope"`
		MemoryID        string                    `json:"memory_id"`
		ExpectedVersion int64                     `json:"expected_version"`
		Patch           httpapi.PatchMemoryFields `json:"patch"`
	}{
		Scope:           scope,
		MemoryID:        memoryID,
		ExpectedVersion: req.ExpectedVersion,
		Patch:           req.Patch,
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func consistencyLevelOrDefault(level string) string {
	if level == "" {
		return "eventual"
	}
	return level
}

func buildRecallCacheKey(scope authz.Scope, req httpapi.RecallUnifiedRequest) string {
	topK := req.TopK
	if topK == 0 {
		topK = 8
	}
	return fmt.Sprintf(
		"%s|%s|%s|%s|%s|%d|%d|%d|%t",
		scope.TenantID,
		scope.UserID,
		scope.ProjectID,
		scope.SessionID,
		strings.TrimSpace(req.Query),
		topK,
		req.TokenBudget,
		req.MemoryTokenBudget,
		req.AllowStaleCache,
	)
}

func hasScopePrefix(key string, scope authz.Scope) bool {
	prefix := fmt.Sprintf("%s|%s|%s|%s|", scope.TenantID, scope.UserID, scope.ProjectID, scope.SessionID)
	return strings.HasPrefix(key, prefix)
}

func resolveScopeVersionStore(store ScopeVersionStore) ScopeVersionStore {
	if store != nil {
		return store
	}
	return newMemoryScopeVersionStore()
}

func resolveSessionIdleTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return 30 * time.Minute
	}
	return ttl
}

func (s *Service) invalidateScopeState(ctx context.Context, scope authz.Scope) {
	s.recallCache.InvalidateScope(scope)
	s.recallCacheObserver.ObserveRecallCache(ctx, RecallCacheObservation{
		Action: "invalidate",
		Scope:  scope,
	})
	if s.scopeVersionStore != nil {
		_, _ = s.scopeVersionStore.BumpScopeVersion(ctx, scope)
	}
}

func (s *Service) validateSessionState(state SessionState, ok bool, now time.Time) error {
	if !ok {
		return nil
	}
	if state.Status == "closed" {
		return httpapi.ErrForbidden("session is closed", map[string]any{
			"session_id": state.SessionID,
			"closed_at":  state.ClosedAt.Format(time.RFC3339),
			"mode":       state.Mode,
		})
	}
	if state.Status == "active" && s.isSessionExpired(state, now) {
		return httpapi.ErrSessionExpired("session is expired", map[string]any{
			"session_id":        state.SessionID,
			"last_heartbeat_at": state.LastHeartbeatAt.Format(time.RFC3339),
			"session_idle_ttl":  s.sessionIdleTTL.String(),
		})
	}
	return nil
}

func (s *Service) isSessionExpired(state SessionState, now time.Time) bool {
	if state.Status != "active" {
		return false
	}
	if state.LastHeartbeatAt.IsZero() {
		return false
	}
	return now.Sub(state.LastHeartbeatAt) > s.sessionIdleTTL
}

func (s *Service) validateBoundedCachedResponse(ctx context.Context, scope authz.Scope, cached httpapi.RecallUnifiedResponse) (bool, error) {
	for _, hit := range cached.Hits {
		record, err := s.backend.GetRecord(ctx, scope.TenantID, hit.MemoryID)
		if err != nil {
			if errors.Is(err, pgmemory.ErrNotFound) {
				return false, nil
			}
			return false, err
		}
		if record.Version != hit.Version {
			return false, nil
		}
	}
	return true, nil
}

func translateBackendError(err error) error {
	switch {
	case errors.Is(err, pgmemory.ErrVersionConflict):
		return httpapi.ErrMemoryConflict("expected_version does not match current version", nil)
	case errors.Is(err, pgmemory.ErrIdempotencyConflict):
		return httpapi.ErrIdempotencyConflict("idempotency_key conflicts with an existing payload", nil)
	case errors.Is(err, pgmemory.ErrNotFound):
		return httpapi.ErrNotFound("memory record not found", nil)
	case err != nil:
		return httpapi.ErrUpstreamUnavailable("memory backend is unavailable", map[string]any{"cause": err.Error()})
	default:
		return fmt.Errorf("memory-gateway/service: %w", err)
	}
}
