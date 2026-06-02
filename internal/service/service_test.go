package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	"github.com/costa92/llm-agent-memory-gateway/internal/httpapi"
	pgmemory "github.com/costa92/llm-agent-memory-postgres/postgres"
)

type fakeBackend struct {
	writeInput corememory.WriteRecordInput
	writeCalls int
	writeErr   error
	writeRes   corememory.WriteRecordResult
	records    map[string]corememory.MemoryRecord
	getErr     error
}

func (f *fakeBackend) GetRecord(_ context.Context, _ string, memoryID string) (corememory.MemoryRecord, error) {
	if f.getErr != nil {
		return corememory.MemoryRecord{}, f.getErr
	}
	if f.records == nil {
		return corememory.MemoryRecord{}, pgmemory.ErrNotFound
	}
	record, ok := f.records[memoryID]
	if ok {
		return record, nil
	}
	return corememory.MemoryRecord{}, pgmemory.ErrNotFound
}

func (f *fakeBackend) GetRecordIncludingHidden(_ context.Context, _ string, memoryID string) (corememory.MemoryRecord, error) {
	if f.getErr != nil {
		return corememory.MemoryRecord{}, f.getErr
	}
	if f.records == nil {
		return corememory.MemoryRecord{}, pgmemory.ErrNotFound
	}
	record, ok := f.records[memoryID]
	if ok {
		return record, nil
	}
	return corememory.MemoryRecord{}, pgmemory.ErrNotFound
}

func (f *fakeBackend) WriteRecord(_ context.Context, in corememory.WriteRecordInput) (corememory.WriteRecordResult, error) {
	f.writeCalls++
	f.writeInput = in
	if f.writeErr != nil {
		return corememory.WriteRecordResult{}, f.writeErr
	}
	if f.writeRes.MemoryID == "" {
		f.writeRes = corememory.WriteRecordResult{
			MemoryID: "mem_123",
			Version:  1,
			Created:  true,
			Record:   in.Record,
		}
	}
	return f.writeRes, nil
}

func (f *fakeBackend) PatchRecord(context.Context, corememory.PatchRecordInput) (corememory.PatchRecordResult, error) {
	return corememory.PatchRecordResult{}, nil
}

func (f *fakeBackend) DeleteRecord(context.Context, corememory.DeleteRecordInput) (corememory.DeleteRecordResult, error) {
	return corememory.DeleteRecordResult{
		MemoryID: "mem_123",
		Version:  2,
		Record: corememory.MemoryRecord{
			MemoryID: "mem_123",
			Deleted:  true,
			Version:  2,
		},
	}, nil
}

func (f *fakeBackend) PinRecord(_ context.Context, in corememory.PinRecordInput) (corememory.PinRecordResult, error) {
	return corememory.PinRecordResult{
		MemoryID: "mem_123",
		Version:  2,
		Record: corememory.MemoryRecord{
			MemoryID: "mem_123",
			Pinned:   in.Pinned,
			Version:  2,
		},
	}, nil
}

func (f *fakeBackend) DisableRecord(_ context.Context, in corememory.DisableRecordInput) (corememory.DisableRecordResult, error) {
	return corememory.DisableRecordResult{
		MemoryID: "mem_123",
		Version:  2,
		Record: corememory.MemoryRecord{
			MemoryID: "mem_123",
			Disabled: in.Disabled,
			Version:  2,
		},
	}, nil
}

type fakeRecaller struct {
	records []corememory.MemoryRecord
	err     error
	calls   int
}

func (f *fakeRecaller) Recall(context.Context, authz.Scope, string, int) ([]corememory.MemoryRecord, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.records, nil
}

type fakeSessionCloser struct {
	scope authz.Scope
	mode  string
	calls int
	err   error
}

func (f *fakeSessionCloser) CloseSession(_ context.Context, scope authz.Scope, mode string) error {
	f.calls++
	f.scope = scope
	f.mode = mode
	return f.err
}

type traceEvent struct {
	stage  string
	fields map[string]any
}

type fakeTraceEmitter struct {
	events []traceEvent
}

func (f *fakeTraceEmitter) Emit(_ context.Context, stage string, fields map[string]any) {
	f.events = append(f.events, traceEvent{stage: stage, fields: fields})
}

func TestWriteMemory_ReadOnlyModeBlocksMutation(t *testing.T) {
	svc, err := New(&fakeBackend{}, nil, nil, nil, Config{ReadOnly: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = svc.WriteMemory(context.Background(), authz.Scope{
		TenantID: "tenant-auth",
		UserID:   "user-auth",
	}, httpapi.WriteMemoryRequest{
		IdempotencyKey: "idem_1",
		Record: httpapi.WriteRecordPayload{
			Content: "remember this",
		},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := httpapi.StatusCode(err); got != 503 {
		t.Fatalf("StatusCode(err) = %d, want 503", got)
	}
}

func TestRecallUnified_EmitsTraceStages(t *testing.T) {
	trace := &fakeTraceEmitter{}
	svc, err := New(
		&fakeBackend{},
		&fakeRecaller{
			records: []corememory.MemoryRecord{
				{MemoryID: "mem_short", Content: "short note", Kind: "semantic", Source: "user_saved", Category: "project", Version: 1},
				{MemoryID: "mem_long", Content: strings.Repeat("very long note ", 20), Kind: "episodic", Source: "agent_inferred", Category: "project", Version: 2},
			},
		},
		nil,
		trace,
		Config{},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := svc.RecallUnified(context.Background(), authz.Scope{
		TenantID: "tenant-auth",
		UserID:   "user-auth",
	}, httpapi.RecallUnifiedRequest{
		Query:             "pdf export",
		MemoryTokenBudget: 10,
		Debug:             true,
	})
	if err != nil {
		t.Fatalf("RecallUnified() error = %v", err)
	}
	if len(resp.Hits) != 1 {
		t.Fatalf("len(resp.Hits) = %d, want 1", len(resp.Hits))
	}

	stages := map[string]bool{}
	for _, event := range trace.events {
		stages[event.stage] = true
	}
	for _, want := range []string{"recalled", "selected", "dropped", "promote_decided"} {
		if !stages[want] {
			t.Fatalf("missing trace stage %q", want)
		}
	}
}

func TestRecallUnified_PrefersShortHighValueHitsWithinBudget(t *testing.T) {
	svc, err := New(
		&fakeBackend{},
		&fakeRecaller{
			records: []corememory.MemoryRecord{
				{
					MemoryID:   "mem_long_pinned",
					Content:    strings.Repeat("long note ", 20),
					Kind:       "semantic",
					Source:     "user_saved",
					Category:   "project",
					Version:    1,
					Pinned:     true,
					Importance: 0.9,
				},
				{
					MemoryID:   "mem_short_pinned",
					Content:    "concise preference",
					Kind:       "semantic",
					Source:     "user_saved",
					Category:   "project",
					Version:    2,
					Pinned:     true,
					Importance: 0.7,
				},
				{
					MemoryID:   "mem_short_agent",
					Content:    "recent inferred note",
					Kind:       "episodic",
					Source:     "agent_inferred",
					Category:   "project",
					Version:    3,
					Pinned:     false,
					Importance: 0.95,
				},
			},
		},
		nil,
		nil,
		Config{},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := svc.RecallUnified(context.Background(), authz.Scope{
		TenantID: "tenant-auth",
		UserID:   "user-auth",
	}, httpapi.RecallUnifiedRequest{
		Query:             "preference",
		MemoryTokenBudget: 8,
	})
	if err != nil {
		t.Fatalf("RecallUnified() error = %v", err)
	}
	if len(resp.Hits) != 1 {
		t.Fatalf("len(resp.Hits) = %d, want 1", len(resp.Hits))
	}
	if resp.Hits[0].MemoryID != "mem_short_pinned" {
		t.Fatalf("selected memory_id = %q, want mem_short_pinned", resp.Hits[0].MemoryID)
	}
}

func TestWriteMemory_AppliesAuthoritativeScopeBeforeBackendCall(t *testing.T) {
	backend := &fakeBackend{}
	svc, err := New(backend, nil, nil, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = svc.WriteMemory(context.Background(), authz.Scope{
		TenantID:  "tenant-auth",
		UserID:    "user-auth",
		ProjectID: "project-auth",
		SessionID: "session-auth",
	}, httpapi.WriteMemoryRequest{
		IdempotencyKey: "idem_1",
		Scope: httpapi.ScopePayload{
			TenantID:  "tenant-claimed",
			UserID:    "user-claimed",
			ProjectID: "project-claimed",
			SessionID: "session-claimed",
		},
		Record: httpapi.WriteRecordPayload{
			Kind:       "semantic",
			Source:     "user_saved",
			Category:   "project",
			Content:    "remember this",
			Importance: 0.9,
			Pinned:     true,
		},
	})
	if err != nil {
		t.Fatalf("WriteMemory() error = %v", err)
	}

	if backend.writeCalls != 1 {
		t.Fatalf("writeCalls = %d, want 1", backend.writeCalls)
	}
	if backend.writeInput.TenantID != "tenant-auth" {
		t.Fatalf("TenantID = %q, want %q", backend.writeInput.TenantID, "tenant-auth")
	}
	if backend.writeInput.Record.UserID != "user-auth" {
		t.Fatalf("UserID = %q, want %q", backend.writeInput.Record.UserID, "user-auth")
	}
	if backend.writeInput.Record.ProjectID != "project-auth" {
		t.Fatalf("ProjectID = %q, want %q", backend.writeInput.Record.ProjectID, "project-auth")
	}
	if backend.writeInput.Record.SessionID != "session-auth" {
		t.Fatalf("SessionID = %q, want %q", backend.writeInput.Record.SessionID, "session-auth")
	}
	if backend.writeInput.RequestHash == "" {
		t.Fatal("RequestHash is empty")
	}
}

func TestRecallUnified_TranslatesBackendNotFoundToEmptyHits(t *testing.T) {
	svc, err := New(&fakeBackend{}, &fakeRecaller{err: pgmemory.ErrNotFound}, nil, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := svc.RecallUnified(context.Background(), authz.Scope{
		TenantID: "tenant-auth",
		UserID:   "user-auth",
	}, httpapi.RecallUnifiedRequest{Query: "pdf"})
	if err != nil {
		t.Fatalf("RecallUnified() error = %v", err)
	}
	if len(resp.Hits) != 0 {
		t.Fatalf("len(resp.Hits) = %d, want 0", len(resp.Hits))
	}
}

func TestRecallUnified_TranslatesBackendUnavailable(t *testing.T) {
	svc, err := New(&fakeBackend{}, &fakeRecaller{err: errors.New("db unavailable")}, nil, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = svc.RecallUnified(context.Background(), authz.Scope{
		TenantID: "tenant-auth",
		UserID:   "user-auth",
	}, httpapi.RecallUnifiedRequest{Query: "pdf"})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := httpapi.StatusCode(err); got != 503 {
		t.Fatalf("StatusCode(err) = %d, want 503", got)
	}
}

func TestCloseSession_ValidatesModeAndUsesAuthoritativeSessionScope(t *testing.T) {
	sessionCloser := &fakeSessionCloser{}
	svc, err := New(&fakeBackend{}, nil, sessionCloser, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := svc.CloseSession(context.Background(), authz.Scope{
		TenantID:  "tenant-auth",
		UserID:    "user-auth",
		ProjectID: "project-auth",
		SessionID: "session-auth",
	}, "session-path", httpapi.SessionCloseRequest{
		Scope: httpapi.ScopePayload{
			TenantID:  "tenant-claimed",
			UserID:    "user-claimed",
			ProjectID: "project-claimed",
			SessionID: "session-claimed",
		},
		Mode: "promote_and_expire",
	})
	if err != nil {
		t.Fatalf("CloseSession() error = %v", err)
	}
	if resp.SessionID != "session-auth" {
		t.Fatalf("SessionID = %q, want %q", resp.SessionID, "session-auth")
	}
	if sessionCloser.calls != 1 {
		t.Fatalf("calls = %d, want 1", sessionCloser.calls)
	}
	if sessionCloser.mode != "promote_and_expire" {
		t.Fatalf("mode = %q, want promote_and_expire", sessionCloser.mode)
	}
	if sessionCloser.scope.TenantID != "tenant-auth" || sessionCloser.scope.UserID != "user-auth" {
		t.Fatalf("scope = %+v, want authoritative scope", sessionCloser.scope)
	}
	if sessionCloser.scope.SessionID != "session-auth" {
		t.Fatalf("SessionID = %q, want session-auth", sessionCloser.scope.SessionID)
	}
}

func TestCloseSession_RejectsInvalidMode(t *testing.T) {
	svc, err := New(&fakeBackend{}, nil, &fakeSessionCloser{}, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = svc.CloseSession(context.Background(), authz.Scope{
		TenantID: "tenant-auth",
		UserID:   "user-auth",
	}, "sess_1", httpapi.SessionCloseRequest{Mode: "bad-mode"})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := httpapi.StatusCode(err); got != 400 {
		t.Fatalf("StatusCode(err) = %d, want 400", got)
	}
}

func TestCloseSession_RecordsClosedStateAndRecallRejectsClosedSession(t *testing.T) {
	svc, err := New(&fakeBackend{}, &fakeRecaller{}, &fakeSessionCloser{}, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	scope := authz.Scope{
		TenantID:  "tenant-auth",
		UserID:    "user-auth",
		ProjectID: "project-auth",
		SessionID: "session-auth",
	}
	resp, err := svc.CloseSession(context.Background(), scope, "session-path", httpapi.SessionCloseRequest{
		Mode: "expire_working",
	})
	if err != nil {
		t.Fatalf("CloseSession() error = %v", err)
	}
	if resp.Status != "closed" {
		t.Fatalf("status = %q, want closed", resp.Status)
	}
	if svc.sessionRegistry == nil {
		t.Fatal("sessionRegistry is nil")
	}
	state, ok, err := svc.sessionRegistry.Get(context.Background(), scope)
	if err != nil {
		t.Fatalf("sessionRegistry.Get() error = %v", err)
	}
	if !ok {
		t.Fatal("expected session state to exist")
	}
	if state.Mode != "expire_working" || state.Status != "closed" {
		t.Fatalf("state = %+v", state)
	}

	_, err = svc.RecallUnified(context.Background(), scope, httpapi.RecallUnifiedRequest{Query: "pdf"})
	if err == nil {
		t.Fatal("expected closed-session recall error")
	}
	if got := httpapi.StatusCode(err); got != 403 {
		t.Fatalf("StatusCode(err) = %d, want 403", got)
	}
}

func TestCloseSession_IsIdempotentForAlreadyClosedSession(t *testing.T) {
	svc, err := New(&fakeBackend{}, nil, &fakeSessionCloser{}, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	scope := authz.Scope{
		TenantID:  "tenant-auth",
		UserID:    "user-auth",
		ProjectID: "project-auth",
		SessionID: "session-auth",
	}
	if _, err := svc.CloseSession(context.Background(), scope, "session-path", httpapi.SessionCloseRequest{Mode: "promote_and_expire"}); err != nil {
		t.Fatalf("first CloseSession() error = %v", err)
	}
	second, err := svc.CloseSession(context.Background(), scope, "session-path", httpapi.SessionCloseRequest{Mode: "promote_and_expire"})
	if err != nil {
		t.Fatalf("second CloseSession() error = %v", err)
	}
	if second.Status != "closed" || second.SessionID != "session-auth" {
		t.Fatalf("second response = %+v", second)
	}
}

func TestHeartbeatSession_ActivatesOpenSession(t *testing.T) {
	svc, err := New(&fakeBackend{}, nil, &fakeSessionCloser{}, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := svc.HeartbeatSession(context.Background(), authz.Scope{
		TenantID:  "tenant-auth",
		UserID:    "user-auth",
		ProjectID: "project-auth",
		SessionID: "session-auth",
	}, "session-path", httpapi.SessionHeartbeatRequest{})
	if err != nil {
		t.Fatalf("HeartbeatSession() error = %v", err)
	}
	if resp.Status != "active" || resp.SessionID != "session-auth" {
		t.Fatalf("response = %+v", resp)
	}
	state, ok, err := svc.sessionRegistry.Get(context.Background(), authz.Scope{
		TenantID:  "tenant-auth",
		UserID:    "user-auth",
		ProjectID: "project-auth",
		SessionID: "session-auth",
	})
	if err != nil {
		t.Fatalf("sessionRegistry.Get() error = %v", err)
	}
	if !ok {
		t.Fatal("expected session state")
	}
	if !state.ClosedAt.IsZero() {
		t.Fatalf("ClosedAt = %v, want zero", state.ClosedAt)
	}
	if state.LastHeartbeatAt.IsZero() {
		t.Fatal("LastHeartbeatAt was not recorded")
	}
}

func TestHeartbeatSession_RejectsClosedSession(t *testing.T) {
	svc, err := New(&fakeBackend{}, nil, &fakeSessionCloser{}, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	scope := authz.Scope{
		TenantID:  "tenant-auth",
		UserID:    "user-auth",
		ProjectID: "project-auth",
		SessionID: "session-auth",
	}
	if _, err := svc.CloseSession(context.Background(), scope, "session-path", httpapi.SessionCloseRequest{Mode: "expire_working"}); err != nil {
		t.Fatalf("CloseSession() error = %v", err)
	}
	_, err = svc.HeartbeatSession(context.Background(), scope, "session-path", httpapi.SessionHeartbeatRequest{})
	if err == nil {
		t.Fatal("expected heartbeat error")
	}
	if got := httpapi.StatusCode(err); got != 403 {
		t.Fatalf("StatusCode(err) = %d, want 403", got)
	}
}

func TestRecallUnified_RejectsExpiredSession(t *testing.T) {
	store := newFakeSessionStateStore()
	expiredAt := time.Now().UTC().Add(-31 * time.Minute)
	scope := authz.Scope{
		TenantID:  "tenant-auth",
		UserID:    "user-auth",
		ProjectID: "project-auth",
		SessionID: "session-auth",
	}
	store.states[sessionScopeKey(scope)] = SessionState{
		TenantID:        scope.TenantID,
		UserID:          scope.UserID,
		ProjectID:       scope.ProjectID,
		SessionID:       scope.SessionID,
		Status:          "active",
		LastHeartbeatAt: expiredAt,
	}

	svc, err := New(&fakeBackend{}, &fakeRecaller{}, nil, nil, Config{
		SessionStateStore: store,
		SessionIdleTTL:    30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = svc.RecallUnified(context.Background(), scope, httpapi.RecallUnifiedRequest{Query: "pdf"})
	if err == nil {
		t.Fatal("expected expired-session recall error")
	}
	if got := httpapi.StatusCode(err); got != 403 {
		t.Fatalf("StatusCode(err) = %d, want 403", got)
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Fatalf("error = %v, want expired", err)
	}
}

func TestHeartbeatSession_RejectsExpiredSession(t *testing.T) {
	store := newFakeSessionStateStore()
	expiredAt := time.Now().UTC().Add(-31 * time.Minute)
	scope := authz.Scope{
		TenantID:  "tenant-auth",
		UserID:    "user-auth",
		ProjectID: "project-auth",
		SessionID: "session-auth",
	}
	store.states[sessionScopeKey(scope)] = SessionState{
		TenantID:        scope.TenantID,
		UserID:          scope.UserID,
		ProjectID:       scope.ProjectID,
		SessionID:       scope.SessionID,
		Status:          "active",
		LastHeartbeatAt: expiredAt,
	}

	svc, err := New(&fakeBackend{}, nil, &fakeSessionCloser{}, nil, Config{
		SessionStateStore: store,
		SessionIdleTTL:    30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = svc.HeartbeatSession(context.Background(), scope, "session-path", httpapi.SessionHeartbeatRequest{})
	if err == nil {
		t.Fatal("expected expired-session heartbeat error")
	}
	if got := httpapi.StatusCode(err); got != 403 {
		t.Fatalf("StatusCode(err) = %d, want 403", got)
	}
}

func TestNew_DefaultsSessionIdleTTL(t *testing.T) {
	svc, err := New(&fakeBackend{}, nil, nil, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if svc.sessionIdleTTL != 30*time.Minute {
		t.Fatalf("sessionIdleTTL = %v, want %v", svc.sessionIdleTTL, 30*time.Minute)
	}
}

func TestCloseSession_ClosesPreviouslyActiveSession(t *testing.T) {
	svc, err := New(&fakeBackend{}, nil, &fakeSessionCloser{}, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	scope := authz.Scope{
		TenantID:  "tenant-auth",
		UserID:    "user-auth",
		ProjectID: "project-auth",
		SessionID: "session-auth",
	}
	if _, err := svc.HeartbeatSession(context.Background(), scope, "session-path", httpapi.SessionHeartbeatRequest{}); err != nil {
		t.Fatalf("HeartbeatSession() error = %v", err)
	}
	resp, err := svc.CloseSession(context.Background(), scope, "session-path", httpapi.SessionCloseRequest{Mode: "expire_working"})
	if err != nil {
		t.Fatalf("CloseSession() error = %v", err)
	}
	if resp.Status != "closed" {
		t.Fatalf("response = %+v", resp)
	}

	state, ok, err := svc.sessionRegistry.Get(context.Background(), scope)
	if err != nil {
		t.Fatalf("sessionRegistry.Get() error = %v", err)
	}
	if !ok {
		t.Fatal("expected session state")
	}
	if state.Status != "closed" {
		t.Fatalf("state = %+v", state)
	}
	if state.ClosedAt.IsZero() {
		t.Fatal("ClosedAt was not recorded")
	}
	if state.LastHeartbeatAt.IsZero() {
		t.Fatal("LastHeartbeatAt should be preserved after close")
	}
}

func TestDeleteMemory_ReturnsDeletedMutation(t *testing.T) {
	backend := &fakeBackend{}
	svc, err := New(backend, nil, nil, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := svc.DeleteMemory(context.Background(), authz.Scope{
		TenantID: "tenant-auth",
		UserID:   "user-auth",
	}, "mem_123", httpapi.DeleteMemoryRequest{
		ExpectedVersion: 1,
	})
	if err != nil {
		t.Fatalf("DeleteMemory() error = %v", err)
	}
	if resp.MemoryID != "mem_123" || !resp.Deleted || resp.Version != 2 {
		t.Fatalf("response = %+v", resp)
	}
}

func TestDisableMemory_ReturnsDisabledMutation(t *testing.T) {
	backend := &fakeBackend{}
	svc, err := New(backend, nil, nil, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := svc.DisableMemory(context.Background(), authz.Scope{
		TenantID: "tenant-auth",
		UserID:   "user-auth",
	}, "mem_123", httpapi.DisableMemoryRequest{
		ExpectedVersion: 1,
	})
	if err != nil {
		t.Fatalf("DisableMemory() error = %v", err)
	}
	if !resp.Disabled || resp.MemoryID != "mem_123" || resp.Version != 2 {
		t.Fatalf("response = %+v", resp)
	}
}

func TestUnpinMemory_ReturnsUnpinnedMutation(t *testing.T) {
	backend := &fakeBackend{}
	svc, err := New(backend, nil, nil, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := svc.UnpinMemory(context.Background(), authz.Scope{
		TenantID: "tenant-auth",
		UserID:   "user-auth",
	}, "mem_123", httpapi.PinMemoryRequest{
		ExpectedVersion: 1,
	})
	if err != nil {
		t.Fatalf("UnpinMemory() error = %v", err)
	}
	if resp.Pinned || resp.MemoryID != "mem_123" || resp.Version != 2 {
		t.Fatalf("response = %+v", resp)
	}
}

func TestEnableMemory_ReturnsEnabledMutation(t *testing.T) {
	backend := &fakeBackend{}
	svc, err := New(backend, nil, nil, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := svc.EnableMemory(context.Background(), authz.Scope{
		TenantID: "tenant-auth",
		UserID:   "user-auth",
	}, "mem_123", httpapi.DisableMemoryRequest{
		ExpectedVersion: 1,
	})
	if err != nil {
		t.Fatalf("EnableMemory() error = %v", err)
	}
	if resp.Disabled || resp.MemoryID != "mem_123" || resp.Version != 2 {
		t.Fatalf("response = %+v", resp)
	}
}

func TestGetMemoryItem_ReturnsMappedRecord(t *testing.T) {
	backend := &fakeBackend{
		records: map[string]corememory.MemoryRecord{
			"mem_abc": {
				MemoryID:   "mem_abc",
				Kind:       "semantic",
				Version:    3,
				Content:    "hello world",
				Tags:       []string{"tag1", "tag2"},
				Source:     "user_saved",
				Category:   "project",
				Importance: 0.8,
				Pinned:     true,
				Disabled:   false,
			},
		},
	}
	svc, err := New(backend, nil, nil, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := svc.GetMemoryItem(context.Background(), authz.Scope{
		TenantID: "tenant-auth",
		UserID:   "user-auth",
	}, "mem_abc")
	if err != nil {
		t.Fatalf("GetMemoryItem() error = %v", err)
	}
	if resp.MemoryID != "mem_abc" {
		t.Fatalf("MemoryID = %q, want mem_abc", resp.MemoryID)
	}
	if resp.Kind != "semantic" {
		t.Fatalf("Kind = %q, want semantic", resp.Kind)
	}
	if resp.Version != 3 {
		t.Fatalf("Version = %d, want 3", resp.Version)
	}
	if resp.Content != "hello world" {
		t.Fatalf("Content = %q, want hello world", resp.Content)
	}
	if len(resp.Tags) != 2 || resp.Tags[0] != "tag1" {
		t.Fatalf("Tags = %v, want [tag1 tag2]", resp.Tags)
	}
	if resp.Importance != 0.8 {
		t.Fatalf("Importance = %f, want 0.8", resp.Importance)
	}
	if !resp.Pinned {
		t.Fatal("Pinned = false, want true")
	}
}

func TestGetMemoryItem_NotFound(t *testing.T) {
	backend := &fakeBackend{
		getErr: pgmemory.ErrNotFound,
	}
	svc, err := New(backend, nil, nil, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = svc.GetMemoryItem(context.Background(), authz.Scope{
		TenantID: "tenant-auth",
		UserID:   "user-auth",
	}, "mem_missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := httpapi.StatusCode(err); got != 404 {
		t.Fatalf("StatusCode(err) = %d, want 404", got)
	}
}

// --- M8a: working-kind write-path validation ----------------------------

// TestWriteMemory_AcceptsWorkingKind asserts kind="working" is accepted and
// passed through to the backend unchanged.
func TestWriteMemory_AcceptsWorkingKind(t *testing.T) {
	backend := &fakeBackend{}
	svc, err := New(backend, nil, nil, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = svc.WriteMemory(context.Background(), authz.Scope{
		TenantID: "tenant-auth",
		UserID:   "user-auth",
	}, httpapi.WriteMemoryRequest{
		IdempotencyKey: "idem_working_kind",
		Record: httpapi.WriteRecordPayload{
			Kind:     "working",
			Source:   "agent_inferred",
			Category: "session",
			Content:  "transient note",
		},
	})
	if err != nil {
		t.Fatalf("WriteMemory() error = %v", err)
	}
	if got := backend.writeInput.Record.Kind; got != corememory.RecordKindWorking {
		t.Fatalf("Record.Kind = %q, want %q", got, corememory.RecordKindWorking)
	}
}

// TestWriteMemory_DefaultsBlankKindToEpisodic asserts a blank kind is
// normalized to "episodic" before reaching the backend.
func TestWriteMemory_DefaultsBlankKindToEpisodic(t *testing.T) {
	backend := &fakeBackend{}
	svc, err := New(backend, nil, nil, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = svc.WriteMemory(context.Background(), authz.Scope{
		TenantID: "tenant-auth",
		UserID:   "user-auth",
	}, httpapi.WriteMemoryRequest{
		IdempotencyKey: "idem_blank_kind",
		Record: httpapi.WriteRecordPayload{
			Source:   "user_saved",
			Category: "project",
			Content:  "remember this",
		},
	})
	if err != nil {
		t.Fatalf("WriteMemory() error = %v", err)
	}
	if got := backend.writeInput.Record.Kind; got != corememory.RecordKindEpisodic {
		t.Fatalf("Record.Kind = %q, want %q (blank should default)", got, corememory.RecordKindEpisodic)
	}
}

// TestWriteMemory_RejectsUnknownKind asserts an unrecognized kind is rejected
// with a 400 before the backend is touched.
func TestWriteMemory_RejectsUnknownKind(t *testing.T) {
	backend := &fakeBackend{}
	svc, err := New(backend, nil, nil, nil, Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = svc.WriteMemory(context.Background(), authz.Scope{
		TenantID: "tenant-auth",
		UserID:   "user-auth",
	}, httpapi.WriteMemoryRequest{
		IdempotencyKey: "idem_bad_kind",
		Record: httpapi.WriteRecordPayload{
			Kind:     "archive",
			Source:   "user_saved",
			Category: "project",
			Content:  "bad kind",
		},
	})
	if err == nil {
		t.Fatal("WriteMemory() expected error for unknown kind, got nil")
	}
	if got := httpapi.StatusCode(err); got != 400 {
		t.Fatalf("StatusCode(err) = %d, want 400", got)
	}
	if backend.writeCalls != 0 {
		t.Fatalf("writeCalls = %d, want 0 (backend must not be called on bad kind)", backend.writeCalls)
	}
}
