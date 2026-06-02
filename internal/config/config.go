package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	defaultListenAddr          = ":8080"
	defaultSessionIdleTTL      = 30 * time.Minute
	defaultRecallMode          = "lexical"
	defaultVectorTable         = "memory_gateway_vectors"
	defaultVectorIndex         = "none"
	defaultVectorDim           = 32
	defaultOutboxPoll          = time.Second
	defaultOutboxBatch         = 100
	defaultTraceSinkBufferSize = 1024
	defaultTraceSinkBatchSize  = 50
	defaultTraceSinkShutdown   = 5 * time.Second
	defaultStorageMetricsCycle = 5 * time.Minute
	defaultSessionReaperCycle  = 5 * time.Minute
	defaultHardDeleteRetention = 30 * 24 * time.Hour
	defaultHardDeleteGCCycle   = time.Hour
	defaultRelayLeaseTTL       = 180 * time.Second
	defaultRelayMaxAttempts    = 5
	defaultRelayBatchSize      = 100
)

type Config struct {
	ListenAddr         string
	PostgresURL        string
	ReadOnly           bool
	SessionIdleTTL     time.Duration
	RecallMode         string
	VectorEnabled      bool
	VectorTable        string
	VectorDimension    int
	VectorNamespace    string
	VectorIndex        string
	OutboxPollInterval time.Duration
	OutboxBatchSize    int

	// EmbeddingCostMicrosPerToken is the unit cost (in micro-units) applied to
	// the embedding_cost_total counter on every successful Embed call. The
	// gateway treats "tokens" as the whitespace-separated word count of the
	// embedded text (the rag/embed SDK does not return token counts at v1);
	// see internal/service/vector_projector.go for the count source.
	// Default 0 keeps the counter at zero for deployments that haven't wired
	// up a cost rate yet.
	EmbeddingCostMicrosPerToken uint64

	// TraceSinkBufferSize bounds the in-memory queue of the async
	// decision-trace sink. When the queue is full, Record drops rows
	// non-blockingly and bumps trace_dropped_total{reason="buffer_full"}.
	// Default 1024.
	TraceSinkBufferSize int

	// TraceSinkBatchSize is the maximum rows per INSERT issued by the sink's
	// writer goroutine. Default 50.
	TraceSinkBatchSize int

	// TraceSinkShutdownTimeout bounds the drain budget when the gateway shuts
	// down. Rows that cannot be flushed within the budget are counted as
	// trace_dropped_total{reason="shutdown"}. Default 5s.
	TraceSinkShutdownTimeout time.Duration

	// StorageMetricsInterval is the period at which the storage-bytes cron
	// re-queries Postgres and updates memory_storage_bytes_total /
	// vector_storage_bytes_total. Default 5m.
	StorageMetricsInterval time.Duration

	// SessionReaperEnabled turns on the background orphaned-session reaper
	// (D6), which reclaims the working memory of sessions that went idle
	// beyond SessionIdleTTL but were never explicitly closed. Default true —
	// without it, working records of abandoned sessions accumulate forever.
	// Env `LLM_AGENT_MEMORY_GATEWAY_SESSION_REAPER_ENABLED`.
	SessionReaperEnabled bool

	// SessionReaperInterval is how often the reaper scans for idle sessions.
	// The idle cutoff itself is SessionIdleTTL. Default 5m. Env
	// `LLM_AGENT_MEMORY_GATEWAY_SESSION_REAPER_INTERVAL`.
	SessionReaperInterval time.Duration

	// HardDeleteGCEnabled turns on the hard-delete GC (D4): a background job
	// that physically removes rows soft-deleted longer ago than
	// HardDeleteRetention. Soft-deleted rows are already invisible to queries,
	// so this only reclaims storage. **Default false** — it is an irreversible
	// physical delete; operators opt in. Env
	// `LLM_AGENT_MEMORY_GATEWAY_HARD_DELETE_GC_ENABLED`.
	HardDeleteGCEnabled bool

	// HardDeleteRetention is how long a soft-deleted row is kept before the GC
	// physically removes it. Default 30 days (720h). Env
	// `LLM_AGENT_MEMORY_GATEWAY_HARD_DELETE_RETENTION`.
	HardDeleteRetention time.Duration

	// HardDeleteGCInterval is how often the GC runs. Default 1h. Env
	// `LLM_AGENT_MEMORY_GATEWAY_HARD_DELETE_GC_INTERVAL`.
	HardDeleteGCInterval time.Duration

	// TraceRetentionEnabled is a forward-looking flag (M8) for operator-side
	// retention of memory_decision_trace rows. v1 (M7) leaves this off and
	// treats retention as an operator obligation — see spec OD-5. Default
	// false.
	TraceRetentionEnabled bool

	// RelayLeaseTTL bounds how long a single relay worker may hold a
	// claimed outbox row before a peer can reclaim it. Default 180s; tune
	// down for faster recovery from crashed pods (at the cost of more
	// thrash if publishes are slow). Env
	// `LLM_AGENT_MEMORY_GATEWAY_RELAY_LEASE_TTL`.
	//
	// Deployment guidance: `terminationGracePeriodSeconds` must be
	// >= RelayLeaseTTL + ~10s slack so Release(ctx) can flip owned leases
	// back to pending before the pod is killed.
	RelayLeaseTTL time.Duration

	// RelayMaxAttempts is the per-row retry budget the relay enforces.
	// When attempt_count reaches this value and publish still fails, the
	// row transitions to 'failed' and waits for an operator to
	// RequeueFailed it. Default 5.
	// Env `LLM_AGENT_MEMORY_GATEWAY_RELAY_MAX_ATTEMPTS`.
	RelayMaxAttempts int

	// RelayBatchSize is the maximum rows a single ClaimBatch will lock.
	// Default 100. Env `LLM_AGENT_MEMORY_GATEWAY_RELAY_BATCH_SIZE`.
	// This supersedes OutboxBatchSize for the M8a-prep relay; the older
	// knob is kept for compatibility but RelayBatchSize is the canonical
	// source.
	RelayBatchSize int
}

func LoadFromEnv() (Config, error) {
	cfg := Config{
		ListenAddr:               defaultListenAddr,
		PostgresURL:              os.Getenv("LLM_AGENT_MEMORY_PG_URL"),
		SessionIdleTTL:           defaultSessionIdleTTL,
		RecallMode:               defaultRecallMode,
		VectorTable:              defaultVectorTable,
		VectorDimension:          defaultVectorDim,
		VectorIndex:              defaultVectorIndex,
		OutboxPollInterval:       defaultOutboxPoll,
		OutboxBatchSize:          defaultOutboxBatch,
		TraceSinkBufferSize:      defaultTraceSinkBufferSize,
		TraceSinkBatchSize:       defaultTraceSinkBatchSize,
		TraceSinkShutdownTimeout: defaultTraceSinkShutdown,
		StorageMetricsInterval:   defaultStorageMetricsCycle,
		SessionReaperEnabled:     true,
		SessionReaperInterval:    defaultSessionReaperCycle,
		HardDeleteRetention:      defaultHardDeleteRetention,
		HardDeleteGCInterval:     defaultHardDeleteGCCycle,
		RelayLeaseTTL:            defaultRelayLeaseTTL,
		RelayMaxAttempts:         defaultRelayMaxAttempts,
		RelayBatchSize:           defaultRelayBatchSize,
	}

	if listenAddr := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_ADDR"); listenAddr != "" {
		cfg.ListenAddr = listenAddr
	}

	if cfg.PostgresURL == "" {
		return Config{}, errors.New("LLM_AGENT_MEMORY_PG_URL is required")
	}

	if readOnlyValue := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_READ_ONLY"); readOnlyValue != "" {
		readOnly, err := strconv.ParseBool(readOnlyValue)
		if err != nil {
			return Config{}, fmt.Errorf("parse LLM_AGENT_MEMORY_GATEWAY_READ_ONLY: %w", err)
		}
		cfg.ReadOnly = readOnly
	}

	if ttlValue := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_SESSION_IDLE_TTL"); ttlValue != "" {
		ttl, err := time.ParseDuration(ttlValue)
		if err != nil {
			return Config{}, fmt.Errorf("parse LLM_AGENT_MEMORY_GATEWAY_SESSION_IDLE_TTL: %w", err)
		}
		if ttl <= 0 {
			return Config{}, errors.New("LLM_AGENT_MEMORY_GATEWAY_SESSION_IDLE_TTL must be > 0")
		}
		cfg.SessionIdleTTL = ttl
	}

	if recallMode := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_RECALL_MODE"); recallMode != "" {
		switch recallMode {
		case "lexical", "hybrid":
			cfg.RecallMode = recallMode
		default:
			return Config{}, fmt.Errorf("LLM_AGENT_MEMORY_GATEWAY_RECALL_MODE must be lexical or hybrid")
		}
	}

	if vectorEnabledValue := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_ENABLED"); vectorEnabledValue != "" {
		vectorEnabled, err := strconv.ParseBool(vectorEnabledValue)
		if err != nil {
			return Config{}, fmt.Errorf("parse LLM_AGENT_MEMORY_GATEWAY_VECTOR_ENABLED: %w", err)
		}
		cfg.VectorEnabled = vectorEnabled
	}

	if vectorTable := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_TABLE"); vectorTable != "" {
		cfg.VectorTable = vectorTable
	}

	if vectorDimensionValue := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_DIMENSION"); vectorDimensionValue != "" {
		vectorDimension, err := strconv.Atoi(vectorDimensionValue)
		if err != nil {
			return Config{}, fmt.Errorf("parse LLM_AGENT_MEMORY_GATEWAY_VECTOR_DIMENSION: %w", err)
		}
		if vectorDimension <= 0 {
			return Config{}, errors.New("LLM_AGENT_MEMORY_GATEWAY_VECTOR_DIMENSION must be > 0")
		}
		cfg.VectorDimension = vectorDimension
	}

	if vectorNamespace := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_NAMESPACE"); vectorNamespace != "" {
		cfg.VectorNamespace = vectorNamespace
	}

	if vectorIndex := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_INDEX"); vectorIndex != "" {
		switch vectorIndex {
		case "none", "ivfflat", "hnsw":
			cfg.VectorIndex = vectorIndex
		default:
			return Config{}, fmt.Errorf("LLM_AGENT_MEMORY_GATEWAY_VECTOR_INDEX must be none, ivfflat, or hnsw")
		}
	}

	if outboxPollValue := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_OUTBOX_POLL_INTERVAL"); outboxPollValue != "" {
		pollInterval, err := time.ParseDuration(outboxPollValue)
		if err != nil {
			return Config{}, fmt.Errorf("parse LLM_AGENT_MEMORY_GATEWAY_OUTBOX_POLL_INTERVAL: %w", err)
		}
		if pollInterval <= 0 {
			return Config{}, errors.New("LLM_AGENT_MEMORY_GATEWAY_OUTBOX_POLL_INTERVAL must be > 0")
		}
		cfg.OutboxPollInterval = pollInterval
	}

	if outboxBatchValue := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_OUTBOX_BATCH_SIZE"); outboxBatchValue != "" {
		batchSize, err := strconv.Atoi(outboxBatchValue)
		if err != nil {
			return Config{}, fmt.Errorf("parse LLM_AGENT_MEMORY_GATEWAY_OUTBOX_BATCH_SIZE: %w", err)
		}
		if batchSize <= 0 {
			return Config{}, errors.New("LLM_AGENT_MEMORY_GATEWAY_OUTBOX_BATCH_SIZE must be > 0")
		}
		cfg.OutboxBatchSize = batchSize
	}

	if costValue := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_EMBED_COST_MICROS"); costValue != "" {
		cost, err := strconv.ParseUint(costValue, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("parse LLM_AGENT_MEMORY_GATEWAY_EMBED_COST_MICROS: %w", err)
		}
		cfg.EmbeddingCostMicrosPerToken = cost
	}

	if bufValue := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_TRACE_BUFFER"); bufValue != "" {
		bufSize, err := strconv.Atoi(bufValue)
		if err != nil {
			return Config{}, fmt.Errorf("parse LLM_AGENT_MEMORY_GATEWAY_TRACE_BUFFER: %w", err)
		}
		if bufSize <= 0 {
			return Config{}, errors.New("LLM_AGENT_MEMORY_GATEWAY_TRACE_BUFFER must be > 0")
		}
		cfg.TraceSinkBufferSize = bufSize
	}

	if batchValue := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_TRACE_BATCH"); batchValue != "" {
		batchSize, err := strconv.Atoi(batchValue)
		if err != nil {
			return Config{}, fmt.Errorf("parse LLM_AGENT_MEMORY_GATEWAY_TRACE_BATCH: %w", err)
		}
		if batchSize <= 0 {
			return Config{}, errors.New("LLM_AGENT_MEMORY_GATEWAY_TRACE_BATCH must be > 0")
		}
		cfg.TraceSinkBatchSize = batchSize
	}

	if shutValue := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_TRACE_SHUTDOWN"); shutValue != "" {
		shut, err := time.ParseDuration(shutValue)
		if err != nil {
			return Config{}, fmt.Errorf("parse LLM_AGENT_MEMORY_GATEWAY_TRACE_SHUTDOWN: %w", err)
		}
		if shut <= 0 {
			return Config{}, errors.New("LLM_AGENT_MEMORY_GATEWAY_TRACE_SHUTDOWN must be > 0")
		}
		cfg.TraceSinkShutdownTimeout = shut
	}

	if storageValue := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_STORAGE_INTERVAL"); storageValue != "" {
		interval, err := time.ParseDuration(storageValue)
		if err != nil {
			return Config{}, fmt.Errorf("parse LLM_AGENT_MEMORY_GATEWAY_STORAGE_INTERVAL: %w", err)
		}
		if interval <= 0 {
			return Config{}, errors.New("LLM_AGENT_MEMORY_GATEWAY_STORAGE_INTERVAL must be > 0")
		}
		cfg.StorageMetricsInterval = interval
	}

	if reaperEnabledValue := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_SESSION_REAPER_ENABLED"); reaperEnabledValue != "" {
		reaperEnabled, err := strconv.ParseBool(reaperEnabledValue)
		if err != nil {
			return Config{}, fmt.Errorf("parse LLM_AGENT_MEMORY_GATEWAY_SESSION_REAPER_ENABLED: %w", err)
		}
		cfg.SessionReaperEnabled = reaperEnabled
	}

	if reaperIntervalValue := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_SESSION_REAPER_INTERVAL"); reaperIntervalValue != "" {
		interval, err := time.ParseDuration(reaperIntervalValue)
		if err != nil {
			return Config{}, fmt.Errorf("parse LLM_AGENT_MEMORY_GATEWAY_SESSION_REAPER_INTERVAL: %w", err)
		}
		if interval <= 0 {
			return Config{}, errors.New("LLM_AGENT_MEMORY_GATEWAY_SESSION_REAPER_INTERVAL must be > 0")
		}
		cfg.SessionReaperInterval = interval
	}

	if gcEnabledValue := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_HARD_DELETE_GC_ENABLED"); gcEnabledValue != "" {
		gcEnabled, err := strconv.ParseBool(gcEnabledValue)
		if err != nil {
			return Config{}, fmt.Errorf("parse LLM_AGENT_MEMORY_GATEWAY_HARD_DELETE_GC_ENABLED: %w", err)
		}
		cfg.HardDeleteGCEnabled = gcEnabled
	}

	if gcRetentionValue := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_HARD_DELETE_RETENTION"); gcRetentionValue != "" {
		retention, err := time.ParseDuration(gcRetentionValue)
		if err != nil {
			return Config{}, fmt.Errorf("parse LLM_AGENT_MEMORY_GATEWAY_HARD_DELETE_RETENTION: %w", err)
		}
		if retention <= 0 {
			return Config{}, errors.New("LLM_AGENT_MEMORY_GATEWAY_HARD_DELETE_RETENTION must be > 0")
		}
		cfg.HardDeleteRetention = retention
	}

	if gcIntervalValue := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_HARD_DELETE_GC_INTERVAL"); gcIntervalValue != "" {
		interval, err := time.ParseDuration(gcIntervalValue)
		if err != nil {
			return Config{}, fmt.Errorf("parse LLM_AGENT_MEMORY_GATEWAY_HARD_DELETE_GC_INTERVAL: %w", err)
		}
		if interval <= 0 {
			return Config{}, errors.New("LLM_AGENT_MEMORY_GATEWAY_HARD_DELETE_GC_INTERVAL must be > 0")
		}
		cfg.HardDeleteGCInterval = interval
	}

	if retentionValue := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_TRACE_RETENTION"); retentionValue != "" {
		retention, err := strconv.ParseBool(retentionValue)
		if err != nil {
			return Config{}, fmt.Errorf("parse LLM_AGENT_MEMORY_GATEWAY_TRACE_RETENTION: %w", err)
		}
		cfg.TraceRetentionEnabled = retention
	}

	if leaseTTLValue := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_RELAY_LEASE_TTL"); leaseTTLValue != "" {
		ttl, err := time.ParseDuration(leaseTTLValue)
		if err != nil {
			return Config{}, fmt.Errorf("parse LLM_AGENT_MEMORY_GATEWAY_RELAY_LEASE_TTL: %w", err)
		}
		if ttl <= 0 {
			return Config{}, errors.New("LLM_AGENT_MEMORY_GATEWAY_RELAY_LEASE_TTL must be > 0")
		}
		cfg.RelayLeaseTTL = ttl
	}

	if maxAttemptsValue := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_RELAY_MAX_ATTEMPTS"); maxAttemptsValue != "" {
		max, err := strconv.Atoi(maxAttemptsValue)
		if err != nil {
			return Config{}, fmt.Errorf("parse LLM_AGENT_MEMORY_GATEWAY_RELAY_MAX_ATTEMPTS: %w", err)
		}
		if max <= 0 {
			return Config{}, errors.New("LLM_AGENT_MEMORY_GATEWAY_RELAY_MAX_ATTEMPTS must be > 0")
		}
		cfg.RelayMaxAttempts = max
	}

	if batchValue := os.Getenv("LLM_AGENT_MEMORY_GATEWAY_RELAY_BATCH_SIZE"); batchValue != "" {
		size, err := strconv.Atoi(batchValue)
		if err != nil {
			return Config{}, fmt.Errorf("parse LLM_AGENT_MEMORY_GATEWAY_RELAY_BATCH_SIZE: %w", err)
		}
		if size <= 0 {
			return Config{}, errors.New("LLM_AGENT_MEMORY_GATEWAY_RELAY_BATCH_SIZE must be > 0")
		}
		cfg.RelayBatchSize = size
	}

	return cfg, nil
}
