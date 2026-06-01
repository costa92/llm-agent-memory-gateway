package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	"github.com/costa92/llm-agent-memory-gateway/internal/config"
	"github.com/costa92/llm-agent-memory-gateway/internal/service"
	pgmemory "github.com/costa92/llm-agent-memory-postgres/postgres"
	ragpg "github.com/costa92/llm-agent-rag/postgres"
)

type fakeMigrator struct {
	calls int
	err   error
}

func (f *fakeMigrator) Migrate(context.Context) error {
	f.calls++
	return f.err
}

func TestCommandPackageCompiles(t *testing.T) {}

func TestRunMigrations_InvokesMigrator(t *testing.T) {
	migrator := &fakeMigrator{}

	if err := runMigrations(context.Background(), migrator); err != nil {
		t.Fatalf("runMigrations() error = %v", err)
	}
	if migrator.calls != 1 {
		t.Fatalf("calls = %d, want 1", migrator.calls)
	}
}

func TestRunMigrations_PropagatesError(t *testing.T) {
	migrator := &fakeMigrator{err: errors.New("boom")}

	if err := runMigrations(context.Background(), migrator); err == nil {
		t.Fatal("expected error")
	}
}

type fakeRecordStore struct{}

func (fakeRecordStore) GetRecord(context.Context, string, string) (corememory.MemoryRecord, error) {
	return corememory.MemoryRecord{}, nil
}

func (fakeRecordStore) GetRecordIncludingHidden(context.Context, string, string) (corememory.MemoryRecord, error) {
	return corememory.MemoryRecord{}, nil
}

func (fakeRecordStore) WriteRecord(context.Context, corememory.WriteRecordInput) (corememory.WriteRecordResult, error) {
	return corememory.WriteRecordResult{}, nil
}

func (fakeRecordStore) PatchRecord(context.Context, corememory.PatchRecordInput) (corememory.PatchRecordResult, error) {
	return corememory.PatchRecordResult{}, nil
}

func (fakeRecordStore) DeleteRecord(context.Context, corememory.DeleteRecordInput) (corememory.DeleteRecordResult, error) {
	return corememory.DeleteRecordResult{}, nil
}

func (fakeRecordStore) PinRecord(context.Context, corememory.PinRecordInput) (corememory.PinRecordResult, error) {
	return corememory.PinRecordResult{}, nil
}

func (fakeRecordStore) DisableRecord(context.Context, corememory.DisableRecordInput) (corememory.DisableRecordResult, error) {
	return corememory.DisableRecordResult{}, nil
}

func TestBuildRecallBackend_SupportsConfiguredModes(t *testing.T) {
	for _, mode := range []string{"lexical", "hybrid"} {
		t.Run(mode, func(t *testing.T) {
			backend := buildRecallBackend(nil, fakeRecordStore{}, config.Config{RecallMode: mode}, nil)
			if backend == nil {
				t.Fatal("backend is nil")
			}
		})
	}
}

func TestBuildVectorCandidateSource_DisabledReturnsNullSource(t *testing.T) {
	source, cleanup, err := buildVectorCandidateSource(context.Background(), config.Config{VectorEnabled: false}, nil)
	if err != nil {
		t.Fatalf("buildVectorCandidateSource() error = %v", err)
	}
	if source == nil {
		t.Fatal("source is nil")
	}
	if cleanup != nil {
		t.Fatal("cleanup should be nil when vector is disabled")
	}
}

func TestParseVectorIndex(t *testing.T) {
	if got := parseVectorIndex("none"); got != ragpg.VectorIndexNone {
		t.Fatalf("parseVectorIndex(none) = %v", got)
	}
	if got := parseVectorIndex("ivfflat"); got != ragpg.VectorIndexIVFFlat {
		t.Fatalf("parseVectorIndex(ivfflat) = %v", got)
	}
	if got := parseVectorIndex("hnsw"); got != ragpg.VectorIndexHNSW {
		t.Fatalf("parseVectorIndex(hnsw) = %v", got)
	}
}

type fakeRelayRunner struct {
	calls int
	stats pgmemory.RunStats
	err   error
}

func (f *fakeRelayRunner) RunOnce(context.Context) (pgmemory.RunStats, error) {
	f.calls++
	return f.stats, f.err
}

func TestStartOutboxRelayWorker_RunsRelayUntilCanceled(t *testing.T) {
	runner := &fakeRelayRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	stop := startOutboxRelayWorker(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), 5*time.Millisecond, runner)

	time.Sleep(20 * time.Millisecond)
	cancel()
	stop()

	if runner.calls == 0 {
		t.Fatal("expected relay worker to call RunOnce at least once")
	}
}

func TestStartOutboxRelayWorker_LogsRelayStats(t *testing.T) {
	buf := &bytes.Buffer{}
	runner := &fakeRelayRunner{stats: pgmemory.RunStats{Published: 2, Failed: 1}}
	ctx, cancel := context.WithCancel(context.Background())
	stop := startOutboxRelayWorker(ctx, slog.New(slog.NewTextHandler(buf, nil)), 5*time.Millisecond, runner)

	time.Sleep(10 * time.Millisecond)
	cancel()
	stop()

	if got := buf.String(); got == "" || !containsAll(got, "outbox relay tick", "published=2", "failed=1") {
		t.Fatalf("log output = %q", got)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}

// fakeTraceExecutor captures invocations so the trace insert closure can be
// asserted in tests without a live Postgres pool.
type fakeTraceExecutor struct {
	sqls  []string
	args  [][]any
	calls int
	err   error
}

func (f *fakeTraceExecutor) Exec(_ context.Context, sql string, args ...any) (commandTagDiscard, error) {
	f.calls++
	f.sqls = append(f.sqls, sql)
	f.args = append(f.args, args)
	return commandTagDiscard{}, f.err
}

func TestBuildTraceInsertFunc_BuildsMultiValuesInsert(t *testing.T) {
	exec := &fakeTraceExecutor{}
	insert := buildTraceInsertFunc(exec, "memory_decision_trace")

	rows := []service.TraceRow{
		{
			TenantID:  "tenant_a",
			RequestID: "req_1",
			Stage:     "recalled",
			Reason:    "promote_accept",
			MemoryID:  "mem_1",
			Version:   42,
			EmittedAt: time.Unix(1700000000, 0).UTC(),
			Emitter:   "gateway",
			Payload:   map[string]any{"k": "v"},
		},
		{
			// Test nullable columns: empty request_id, memory_id, zero version.
			TenantID:  "tenant_b",
			Stage:     "promote_decided",
			Reason:    "store",
			EmittedAt: time.Unix(1700000001, 0).UTC(),
			Emitter:   "gateway",
		},
	}

	if err := insert(context.Background(), rows); err != nil {
		t.Fatalf("insert() error = %v", err)
	}
	if exec.calls != 1 {
		t.Fatalf("calls = %d, want 1", exec.calls)
	}
	if !strings.Contains(exec.sqls[0], "INSERT INTO memory_decision_trace") {
		t.Fatalf("sql missing INSERT INTO: %q", exec.sqls[0])
	}
	if !strings.Contains(exec.sqls[0], "($1, $2, $3, $4, $5, $6, $7, $8, $9), ($10,") {
		t.Fatalf("sql missing batched VALUES: %q", exec.sqls[0])
	}
	if got := len(exec.args[0]); got != 18 {
		t.Fatalf("arg count = %d, want 18 (2 rows × 9 cols)", got)
	}
	// Row 1 args: tenant, requestID, stage, reason, memoryID, version, emittedAt, emitter, payload.
	if exec.args[0][0] != "tenant_a" {
		t.Fatalf("row1 tenant = %v, want tenant_a", exec.args[0][0])
	}
	if exec.args[0][1] != "req_1" {
		t.Fatalf("row1 request_id = %v, want req_1", exec.args[0][1])
	}
	if exec.args[0][5] != int64(42) {
		t.Fatalf("row1 version = %v (%T), want int64(42)", exec.args[0][5], exec.args[0][5])
	}
	// Row 1 payload — must be JSON-marshalled bytes so pgx can write it as
	// jsonb without further reflection.
	payloadRaw, ok := exec.args[0][8].([]byte)
	if !ok {
		t.Fatalf("row1 payload = %v (%T), want []byte", exec.args[0][8], exec.args[0][8])
	}
	var decoded map[string]any
	if err := json.Unmarshal(payloadRaw, &decoded); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if decoded["k"] != "v" {
		t.Fatalf("payload[k] = %v, want v", decoded["k"])
	}

	// Row 2 args start at offset 9.
	if exec.args[0][9] != "tenant_b" {
		t.Fatalf("row2 tenant = %v, want tenant_b", exec.args[0][9])
	}
	if exec.args[0][10] != nil {
		t.Fatalf("row2 request_id = %v, want nil (empty)", exec.args[0][10])
	}
	if exec.args[0][13] != nil {
		t.Fatalf("row2 memory_id = %v, want nil (empty)", exec.args[0][13])
	}
	if exec.args[0][14] != nil {
		t.Fatalf("row2 version = %v, want nil (zero)", exec.args[0][14])
	}
	if exec.args[0][17] != nil {
		t.Fatalf("row2 payload = %v, want nil (empty)", exec.args[0][17])
	}
}

func TestBuildTraceInsertFunc_EmptyRowsIsNoOp(t *testing.T) {
	exec := &fakeTraceExecutor{}
	insert := buildTraceInsertFunc(exec, "memory_decision_trace")
	if err := insert(context.Background(), nil); err != nil {
		t.Fatalf("insert(nil) error = %v", err)
	}
	if exec.calls != 0 {
		t.Fatalf("calls = %d, want 0 (empty batch must not hit DB)", exec.calls)
	}
}

func TestBuildTraceInsertFunc_PropagatesExecError(t *testing.T) {
	exec := &fakeTraceExecutor{err: errors.New("boom")}
	insert := buildTraceInsertFunc(exec, "memory_decision_trace")
	err := insert(context.Background(), []service.TraceRow{{Stage: "x", Reason: "y", Emitter: "g"}})
	if err == nil {
		t.Fatal("expected exec error to propagate")
	}
}

// fakeStorageRows is an in-memory implementation of storageQueryRows for tests.
type fakeStorageRows struct {
	data [][2]any // [tenant_id, memory_bytes int64]
	pos  int
	err  error
}

func (r *fakeStorageRows) Next() bool { return r.pos < len(r.data) }
func (r *fakeStorageRows) Scan(dest ...any) error {
	if len(dest) != 2 {
		return errors.New("expected 2 dest")
	}
	*(dest[0].(*string)) = r.data[r.pos][0].(string)
	*(dest[1].(*int64)) = r.data[r.pos][1].(int64)
	r.pos++
	return nil
}
func (r *fakeStorageRows) Err() error { return r.err }
func (r *fakeStorageRows) Close()     {}

type fakeStorageExecutor struct {
	rows *fakeStorageRows
	err  error
}

func (f *fakeStorageExecutor) Query(_ context.Context, _ string, _ ...any) (storageQueryRows, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

func TestBuildStorageMetricsQuery_AggregatesAndZerosVectorBytes(t *testing.T) {
	exec := &fakeStorageExecutor{rows: &fakeStorageRows{
		data: [][2]any{
			{"tenant_a", int64(1024)},
			{"tenant_b", int64(2048)},
		},
	}}
	query := buildStorageMetricsQuery(exec, "memory_record")

	rows, err := query(context.Background())
	if err != nil {
		t.Fatalf("query() error = %v", err)
	}
	if got, want := len(rows), 2; got != want {
		t.Fatalf("rows = %d, want %d", got, want)
	}
	for _, row := range rows {
		if row.VectorBytes != 0 {
			t.Fatalf("VectorBytes = %d, want 0 (v1: vectors live outside this PG)", row.VectorBytes)
		}
	}
	if rows[0].MemoryBytes != 1024 || rows[1].MemoryBytes != 2048 {
		t.Fatalf("memory bytes = %+v", rows)
	}
}

func TestBuildStorageMetricsQuery_PropagatesQueryError(t *testing.T) {
	exec := &fakeStorageExecutor{err: errors.New("boom")}
	query := buildStorageMetricsQuery(exec, "memory_record")
	if _, err := query(context.Background()); err == nil {
		t.Fatal("expected query error to propagate")
	}
}
