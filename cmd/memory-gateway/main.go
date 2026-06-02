package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	"github.com/costa92/llm-agent-memory-gateway/internal/config"
	"github.com/costa92/llm-agent-memory-gateway/internal/observability"
	"github.com/costa92/llm-agent-memory-gateway/internal/service"
	"github.com/costa92/llm-agent-memory-gateway/internal/transport"
	pgmemory "github.com/costa92/llm-agent-memory-postgres/postgres"
	ragembed "github.com/costa92/llm-agent-rag/embed"
	ragpg "github.com/costa92/llm-agent-rag/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Trace + storage tables are produced by llm-agent-memory-postgres without a
// table prefix when the gateway constructs the Store with pgmemory.Config{}.
// We mirror the default names here. If the gateway ever starts honoring a
// non-empty TablePrefix, these constants need to move into a Store accessor
// (or the prefix needs to round-trip through config). The M7 plan explicitly
// keeps llm-agent-memory-postgres frozen after Task 1, so we accept the
// duplication.
const (
	memoryDecisionTraceTableName = "memory_decision_trace"
	memoryRecordTableName        = "memory_record"
)

func main() {
	if err := run(context.Background()); err != nil {
		slog.New(slog.NewTextHandler(os.Stderr, nil)).Error("memory gateway failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	handler, cleanup, err := buildHandler(ctx, logger, cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	server := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: handler,
	}

	logger.Info("starting memory gateway", "addr", cfg.ListenAddr, "read_only", cfg.ReadOnly)
	return server.ListenAndServe()
}

func buildHandler(ctx context.Context, logger *slog.Logger, cfg config.Config) (http.Handler, func(), error) {
	pool, err := pgxpool.New(ctx, cfg.PostgresURL)
	if err != nil {
		return nil, nil, err
	}
	cleanupFns := []func(){pool.Close}

	if err := runMigrations(ctx, service.NewPostgresGatewayMigrator(pool)); err != nil {
		pool.Close()
		return nil, nil, err
	}

	store, err := pgmemory.New(pool, pgmemory.Config{})
	if err != nil {
		pool.Close()
		return nil, nil, err
	}

	metrics := observability.NewMetrics()

	// Trace sink: best-effort async persistence of decision-trace rows. The
	// sink is started in a background goroutine; Stop drains within the
	// configured budget. Lifecycle is wired below so Stop runs BEFORE pool
	// close.
	traceSink := service.NewPostgresDecisionTraceSink(service.PostgresDecisionTraceSinkConfig{
		InsertFunc:      buildTraceInsertFunc(poolTraceInsertExecutor{pool: pool}, memoryDecisionTraceTableName),
		BufferSize:      cfg.TraceSinkBufferSize,
		BatchSize:       cfg.TraceSinkBatchSize,
		ShutdownTimeout: cfg.TraceSinkShutdownTimeout,
	})
	metrics.SetTraceDropSource(traceSink.DroppedSnapshot)
	sinkCtx, sinkCancel := context.WithCancel(context.Background())
	go traceSink.Run(sinkCtx)
	cleanupFns = append(cleanupFns, func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), cfg.TraceSinkShutdownTimeout)
		defer stopCancel()
		traceSink.Stop(stopCtx)
		sinkCancel()
	})
	traceSinkEmitter := observability.NewTraceSinkEmitter(traceSink, "gateway", time.Now)

	// Storage-bytes cron: periodic snapshot of per-tenant memory bytes from
	// the memory_record table. Vector bytes are reported as zero in v1; the
	// vector store lives outside this Postgres database.
	storageCron := service.NewStorageMetricsCron(service.StorageMetricsCronConfig{
		Query:    buildStorageMetricsQuery(poolStorageQueryExecutor{pool: pool}, memoryRecordTableName),
		Metrics:  metrics,
		Interval: cfg.StorageMetricsInterval,
	})
	cronCtx, cronCancel := context.WithCancel(context.Background())
	go storageCron.Run(cronCtx)
	cleanupFns = append(cleanupFns, func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), cfg.TraceSinkShutdownTimeout)
		defer stopCancel()
		storageCron.Stop(stopCtx)
		cronCancel()
	})

	vectorSource, vectorCleanup, err := buildVectorCandidateSource(ctx, cfg, metrics)
	if err != nil {
		for i := len(cleanupFns) - 1; i >= 0; i-- {
			cleanupFns[i]()
		}
		return nil, nil, err
	}
	if vectorCleanup != nil {
		cleanupFns = append(cleanupFns, vectorCleanup)
	}
	svc, err := service.New(
		store,
		buildRecallBackend(pool, store, cfg, vectorSource),
		service.NewPostgresDurableSessionCloser(store, metrics.WorkingLifecycleObserver()),
		observability.ComposeTraceEmitters(
			slogTraceEmitter{logger: logger},
			metrics.TraceEmitter(),
			traceSinkEmitter,
		),
		service.Config{
			ReadOnly:            cfg.ReadOnly,
			SessionStateStore:   service.NewPostgresSessionStateStore(pool),
			ScopeVersionStore:   service.NewPostgresScopeVersionStore(pool),
			SessionIdleTTL:      cfg.SessionIdleTTL,
			RecallObserver:      metrics.RecallObserver(),
			RecallCacheObserver: metrics.RecallCacheObserver(),
			IdempotencyStore:    store,
		},
	)
	if err != nil {
		for i := len(cleanupFns) - 1; i >= 0; i-- {
			cleanupFns[i]()
		}
		return nil, nil, err
	}

	// Orphaned-session reaper (D6): periodically reclaim the working memory of
	// sessions that went idle past SessionIdleTTL but were never explicitly
	// closed. Keyed off the working records themselves (not session-state),
	// since an active session is not registered unless the client heartbeats.
	if cfg.SessionReaperEnabled {
		reaper := service.NewSessionReaperCron(service.SessionReaperCronConfig{
			ListIdle: buildIdleSessionsQuery(poolStorageQueryExecutor{pool: pool}, memoryRecordTableName),
			Close:    svc.ReapIdleSession,
			IdleTTL:  cfg.SessionIdleTTL,
			Interval: cfg.SessionReaperInterval,
			OnError: func(err error) {
				logger.Warn("session reaper tick failed", "error", err)
			},
		})
		reaperCtx, reaperCancel := context.WithCancel(context.Background())
		go reaper.Run(reaperCtx)
		cleanupFns = append(cleanupFns, func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), cfg.TraceSinkShutdownTimeout)
			defer stopCancel()
			reaper.Stop(stopCtx)
			reaperCancel()
		})
	}

	if cfg.VectorEnabled {
		projector := buildVectorProjector(cfg, vectorSource, metrics)
		if projector != nil {
			relayObserver := slogOutboxProjectionObserver{logger: logger}
			relay, err := pgmemory.NewRelay(store, service.NewOutboxVectorPublisher(
				store,
				projector,
				multiOutboxObserver{
					observers: []service.OutboxProjectionObserver{
						relayObserver,
						metrics.OutboxObserver(),
					},
				},
			), pgmemory.RelayConfig{
				BatchSize:   cfg.RelayBatchSize,
				LeaseTTL:    cfg.RelayLeaseTTL,
				MaxAttempts: cfg.RelayMaxAttempts,
			})
			if err != nil {
				for i := len(cleanupFns) - 1; i >= 0; i-- {
					cleanupFns[i]()
				}
				return nil, nil, err
			}
			// Lease release MUST run before pool.Close. cleanupFns is LIFO
			// (see the deferred cleanup at the bottom of buildHandler), and
			// pool.Close is at index 0, so any cleanup appended after this
			// point runs before pool.Close. We register Release here so an
			// in-flight relay tick has its claimed rows flipped back to
			// pending instead of waiting out the full lease TTL.
			cleanupFns = append(cleanupFns, func() {
				releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer releaseCancel()
				if err := relay.Release(releaseCtx); err != nil {
					logger.Error("memory gateway relay release failed", "error", err)
				}
			})
			cleanupFns = append(cleanupFns, startOutboxRelayWorker(ctx, logger, cfg.OutboxPollInterval, relay))
		}
	}

	return transport.NewHandler(svc, func(mux *http.ServeMux) {
			mux.Handle("GET /metrics", metrics.Handler())
		}), func() {
			for i := len(cleanupFns) - 1; i >= 0; i-- {
				cleanupFns[i]()
			}
		}, nil
}

func buildRecallBackend(pool *pgxpool.Pool, store corememory.RecordStore, cfg config.Config, vector service.RecallCandidateSource) service.RecallBackend {
	hydrator := service.NewPostgresRecordHydrator(store)
	lexical := service.NewPostgresLexicalCandidateSource(pool)

	switch cfg.RecallMode {
	case "hybrid":
		return service.NewHybridRecaller(
			hydrator,
			lexical,
			vector,
		)
	default:
		return service.NewHybridRecaller(
			hydrator,
			lexical,
		)
	}
}

func buildVectorCandidateSource(ctx context.Context, cfg config.Config, metrics *observability.Metrics) (service.RecallCandidateSource, func(), error) {
	if !cfg.VectorEnabled {
		return service.NewNullVectorCandidateSource(), nil, nil
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.PostgresURL)
	if err != nil {
		return nil, nil, err
	}
	poolCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return ragpg.RegisterTypes(ctx, conn)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, nil, err
	}

	vectorStore, err := ragpg.New(pool, ragpg.Config{
		Table:       cfg.VectorTable,
		Dimension:   cfg.VectorDimension,
		VectorIndex: parseVectorIndex(cfg.VectorIndex),
	})
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	if err := vectorStore.Migrate(ctx); err != nil {
		pool.Close()
		return nil, nil, err
	}

	source := service.NewRAGStoreVectorCandidateSource(
		ragembed.NewHashEmbedder(cfg.VectorDimension),
		vectorStore,
		cfg.VectorNamespace,
	)
	if metrics != nil {
		source.SetEmbeddingMetrics(metrics, cfg.EmbeddingCostMicrosPerToken)
	}
	return source, pool.Close, nil
}

func buildVectorProjector(cfg config.Config, source service.RecallCandidateSource, metrics *observability.Metrics) service.VectorProjector {
	if !cfg.VectorEnabled {
		return nil
	}
	ragSource, ok := source.(*service.RAGStoreVectorCandidateSource)
	if !ok {
		return nil
	}
	projector := service.NewRAGVectorProjector(
		ragembed.NewHashEmbedder(cfg.VectorDimension),
		ragSource.Store(),
		cfg.VectorNamespace,
	)
	if metrics != nil {
		projector.SetEmbeddingMetrics(metrics, cfg.EmbeddingCostMicrosPerToken)
	}
	return projector
}

// traceInsertExecutor abstracts the pgx pool surface used by the trace insert
// closure so the closure is unit-testable without a live database.
type traceInsertExecutor interface {
	Exec(ctx context.Context, sql string, args ...any) (commandTagDiscard, error)
}

// commandTagDiscard is a tiny shim so traceInsertExecutor doesn't need to
// import pgconn. The actual pgxpool.Pool returns pgconn.CommandTag; we ignore
// the value, so any type works for the interface contract.
type commandTagDiscard struct{}

// poolTraceInsertExecutor adapts a *pgxpool.Pool to traceInsertExecutor.
type poolTraceInsertExecutor struct{ pool *pgxpool.Pool }

func (e poolTraceInsertExecutor) Exec(ctx context.Context, sql string, args ...any) (commandTagDiscard, error) {
	_, err := e.pool.Exec(ctx, sql, args...)
	return commandTagDiscard{}, err
}

// buildTraceInsertFunc returns a closure that batch-inserts TraceRow records
// into the memory_decision_trace table using a single multi-VALUES statement.
// The closure swallows nullable string/int64 columns into proper NULL slots.
//
// The closure is intentionally factored so that tests can wire a fake executor;
// see main_test.go.
func buildTraceInsertFunc(exec traceInsertExecutor, table string) func(ctx context.Context, rows []service.TraceRow) error {
	return func(ctx context.Context, rows []service.TraceRow) error {
		if len(rows) == 0 {
			return nil
		}
		const colsPerRow = 9
		args := make([]any, 0, len(rows)*colsPerRow)
		placeholders := make([]string, 0, len(rows))
		for i, row := range rows {
			base := i*colsPerRow + 1
			placeholders = append(placeholders, fmt.Sprintf(
				"($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
				base, base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8,
			))
			emittedAt := row.EmittedAt
			if emittedAt.IsZero() {
				emittedAt = time.Now().UTC()
			}
			var payloadJSON any
			if len(row.Payload) > 0 {
				raw, err := json.Marshal(row.Payload)
				if err != nil {
					return fmt.Errorf("marshal trace payload: %w", err)
				}
				payloadJSON = raw
			}
			var version any
			if row.Version != 0 {
				version = row.Version
			}
			args = append(args,
				row.TenantID,
				nullIfEmpty(row.RequestID),
				row.Stage,
				row.Reason,
				nullIfEmpty(row.MemoryID),
				version,
				emittedAt,
				row.Emitter,
				payloadJSON,
			)
		}
		sql := fmt.Sprintf(
			`INSERT INTO %s (tenant_id, request_id, stage, reason, memory_id, version, emitted_at, emitter, payload) VALUES %s`,
			table,
			strings.Join(placeholders, ", "),
		)
		_, err := exec.Exec(ctx, sql, args...)
		return err
	}
}

// storageQueryExecutor abstracts the pgx pool query surface so the storage cron
// query closure is unit-testable without a live database.
type storageQueryExecutor interface {
	Query(ctx context.Context, sql string, args ...any) (storageQueryRows, error)
}

// storageQueryRows is the tiny iterator surface we need from pgx.Rows.
type storageQueryRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}

// poolStorageQueryExecutor adapts *pgxpool.Pool to storageQueryExecutor.
type poolStorageQueryExecutor struct{ pool *pgxpool.Pool }

func (e poolStorageQueryExecutor) Query(ctx context.Context, sql string, args ...any) (storageQueryRows, error) {
	rows, err := e.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgxRowsAdapter{rows: rows}, nil
}

// pgxRowsAdapter wraps pgx.Rows to satisfy the local storageQueryRows interface
// (pgx.Rows.Close returns void so it already matches; the wrapper exists just
// to bridge the type identity).
type pgxRowsAdapter struct{ rows pgx.Rows }

func (a pgxRowsAdapter) Next() bool             { return a.rows.Next() }
func (a pgxRowsAdapter) Scan(dest ...any) error { return a.rows.Scan(dest...) }
func (a pgxRowsAdapter) Err() error             { return a.rows.Err() }
func (a pgxRowsAdapter) Close()                 { a.rows.Close() }

// buildStorageMetricsQuery returns a closure that aggregates per-tenant memory
// storage bytes from the memory_record table.
//
// vector_storage_bytes is reported as 0 in v1 because vector embeddings live in
// a separate RAG vector store (the llm-agent-rag/postgres backend), not in this
// Postgres database. The counter exposition still emits zero rows so dashboards
// see a stable shape; M8 may wire the vector store as a second source.
func buildStorageMetricsQuery(exec storageQueryExecutor, memoryTable string) func(ctx context.Context) ([]service.StorageRow, error) {
	return func(ctx context.Context) ([]service.StorageRow, error) {
		sql := fmt.Sprintf(
			`SELECT tenant_id, COALESCE(SUM(octet_length(content)), 0)::bigint AS memory_bytes FROM %s GROUP BY tenant_id`,
			memoryTable,
		)
		rows, err := exec.Query(ctx, sql)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var out []service.StorageRow
		for rows.Next() {
			var tenantID string
			var memoryBytes int64
			if err := rows.Scan(&tenantID, &memoryBytes); err != nil {
				return nil, err
			}
			if memoryBytes < 0 {
				memoryBytes = 0
			}
			out = append(out, service.StorageRow{
				TenantID:    tenantID,
				MemoryBytes: uint64(memoryBytes),
				// VectorBytes is zero in v1 — embeddings are in a separate
				// RAG vector store; see buildStorageMetricsQuery doc comment.
				VectorBytes: 0,
			})
		}
		return out, rows.Err()
	}
}

// buildIdleSessionsQuery returns a closure that lists the scopes of sessions
// whose newest working-record activity is older than cutoff — the orphaned
// sessions the reaper (D6) reclaims. Activity is the most recent of
// created_at / updated_at / last_access_at across the session's live working
// records, so a session being written or recalled keeps advancing past the
// cutoff and is left alone. Records with no session_id cannot be session-closed
// and are excluded.
func buildIdleSessionsQuery(exec storageQueryExecutor, memoryTable string) func(ctx context.Context, cutoff time.Time) ([]service.IdleSessionScope, error) {
	return func(ctx context.Context, cutoff time.Time) ([]service.IdleSessionScope, error) {
		sql := fmt.Sprintf(
			`SELECT tenant_id, user_id, COALESCE(project_id, ''), session_id
			FROM %s
			WHERE kind = $1 AND deleted = FALSE AND disabled = FALSE
			  AND session_id IS NOT NULL AND session_id <> ''
			GROUP BY tenant_id, user_id, project_id, session_id
			HAVING MAX(GREATEST(created_at, updated_at, COALESCE(last_access_at, created_at))) < $2`,
			memoryTable,
		)
		rows, err := exec.Query(ctx, sql, corememory.RecordKindWorking, cutoff)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var out []service.IdleSessionScope
		for rows.Next() {
			var s service.IdleSessionScope
			if err := rows.Scan(&s.TenantID, &s.UserID, &s.ProjectID, &s.SessionID); err != nil {
				return nil, err
			}
			out = append(out, s)
		}
		return out, rows.Err()
	}
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func parseVectorIndex(index string) ragpg.VectorIndex {
	switch index {
	case "ivfflat":
		return ragpg.VectorIndexIVFFlat
	case "hnsw":
		return ragpg.VectorIndexHNSW
	default:
		return ragpg.VectorIndexNone
	}
}

func runMigrations(ctx context.Context, migrator service.Migrator) error {
	if migrator == nil {
		return nil
	}
	return migrator.Migrate(ctx)
}

type slogTraceEmitter struct {
	logger *slog.Logger
}

func (e slogTraceEmitter) Emit(_ context.Context, stage string, fields map[string]any) {
	if e.logger == nil {
		return
	}

	args := make([]any, 0, len(fields)*2+2)
	args = append(args, "stage", stage)
	for key, value := range fields {
		args = append(args, key, value)
	}
	e.logger.Info("memory gateway trace", args...)
}

type relayRunner interface {
	RunOnce(ctx context.Context) (pgmemory.RunStats, error)
}

func startOutboxRelayWorker(parent context.Context, logger *slog.Logger, interval time.Duration, runner relayRunner) func() {
	if runner == nil {
		return func() {}
	}

	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	var once sync.Once

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			stats, err := runner.RunOnce(ctx)
			if err != nil && logger != nil && !errors.Is(err, context.Canceled) {
				logger.Error("memory gateway outbox relay failed", "error", err)
			}
			if logger != nil && (stats.Published > 0 || stats.Failed > 0) {
				logger.Info("memory gateway outbox relay tick", "published", stats.Published, "failed", stats.Failed)
			}

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
}

type slogOutboxProjectionObserver struct {
	logger *slog.Logger
}

func (o slogOutboxProjectionObserver) ObserveProjection(_ context.Context, obs service.OutboxProjectionObservation) {
	if o.logger == nil {
		return
	}
	o.logger.Info(
		"memory gateway outbox projection",
		"status", obs.Status,
		"event_type", obs.EventType,
		"tenant_id", obs.TenantID,
		"memory_id", obs.MemoryID,
		"event_version", obs.EventVersion,
		"current_version", obs.CurrentVersion,
		"reason", obs.Reason,
	)
}

type multiOutboxObserver struct {
	observers []service.OutboxProjectionObserver
}

func (o multiOutboxObserver) ObserveProjection(ctx context.Context, obs service.OutboxProjectionObservation) {
	for _, observer := range o.observers {
		if observer != nil {
			observer.ObserveProjection(ctx, obs)
		}
	}
}
