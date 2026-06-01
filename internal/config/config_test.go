package config

import (
	"testing"
	"time"
)

func TestConfigSurface_Compiles(t *testing.T) {
	var cfg Config
	_ = cfg.ListenAddr
	_ = cfg.PostgresURL
	_ = cfg.ReadOnly
	_ = cfg.SessionIdleTTL
	_ = cfg.RecallMode
	_ = cfg.VectorEnabled
	_ = cfg.VectorTable
	_ = cfg.VectorDimension
	_ = cfg.VectorNamespace
	_ = cfg.VectorIndex
	_ = cfg.OutboxPollInterval
	_ = cfg.OutboxBatchSize
}

func TestLoadFromEnv_RequiresPostgresURL(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_ADDR", "")
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_READ_ONLY", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_RECALL_MODE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_ENABLED", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_TABLE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_DIMENSION", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_NAMESPACE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_INDEX", "")

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error when postgres url is missing")
	}
}

func TestLoadFromEnv_DefaultsListenAddr(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_ADDR", "")
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_READ_ONLY", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_SESSION_IDLE_TTL", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_RECALL_MODE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_ENABLED", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_TABLE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_DIMENSION", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_NAMESPACE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_INDEX", "")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Fatalf("ListenAddr = %q, want %q", cfg.ListenAddr, ":8080")
	}
	if cfg.SessionIdleTTL != 30*time.Minute {
		t.Fatalf("SessionIdleTTL = %v, want %v", cfg.SessionIdleTTL, 30*time.Minute)
	}
	if cfg.RecallMode != "lexical" {
		t.Fatalf("RecallMode = %q, want lexical", cfg.RecallMode)
	}
	if cfg.VectorTable != "memory_gateway_vectors" {
		t.Fatalf("VectorTable = %q, want memory_gateway_vectors", cfg.VectorTable)
	}
	if cfg.VectorDimension != 32 {
		t.Fatalf("VectorDimension = %d, want 32", cfg.VectorDimension)
	}
	if cfg.VectorIndex != "none" {
		t.Fatalf("VectorIndex = %q, want none", cfg.VectorIndex)
	}
	if cfg.OutboxPollInterval != time.Second {
		t.Fatalf("OutboxPollInterval = %v, want %v", cfg.OutboxPollInterval, time.Second)
	}
	if cfg.OutboxBatchSize != 100 {
		t.Fatalf("OutboxBatchSize = %d, want 100", cfg.OutboxBatchSize)
	}
}

func TestLoadFromEnv_ReadOnlyFlag(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_ADDR", ":9090")
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_READ_ONLY", "true")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_SESSION_IDLE_TTL", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_RECALL_MODE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_ENABLED", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_TABLE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_DIMENSION", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_NAMESPACE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_INDEX", "")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if !cfg.ReadOnly {
		t.Fatal("ReadOnly = false, want true")
	}
	if cfg.ListenAddr != ":9090" {
		t.Fatalf("ListenAddr = %q, want %q", cfg.ListenAddr, ":9090")
	}
}

func TestLoadFromEnv_SessionIdleTTL(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_ADDR", "")
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_READ_ONLY", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_SESSION_IDLE_TTL", "45m")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_RECALL_MODE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_ENABLED", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_TABLE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_DIMENSION", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_NAMESPACE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_INDEX", "")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.SessionIdleTTL != 45*time.Minute {
		t.Fatalf("SessionIdleTTL = %v, want %v", cfg.SessionIdleTTL, 45*time.Minute)
	}
}

func TestLoadFromEnv_RejectsNonPositiveSessionIdleTTL(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_ADDR", "")
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_READ_ONLY", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_SESSION_IDLE_TTL", "0s")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_RECALL_MODE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_ENABLED", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_TABLE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_DIMENSION", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_NAMESPACE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_INDEX", "")

	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("expected session idle ttl error")
	}
}

func TestLoadFromEnv_RecallMode(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_ADDR", "")
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_READ_ONLY", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_SESSION_IDLE_TTL", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_RECALL_MODE", "hybrid")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_ENABLED", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_TABLE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_DIMENSION", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_NAMESPACE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_INDEX", "")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.RecallMode != "hybrid" {
		t.Fatalf("RecallMode = %q, want hybrid", cfg.RecallMode)
	}
}

func TestLoadFromEnv_RejectsInvalidRecallMode(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_ADDR", "")
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_READ_ONLY", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_SESSION_IDLE_TTL", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_RECALL_MODE", "vector")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_ENABLED", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_TABLE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_DIMENSION", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_NAMESPACE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_INDEX", "")

	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("expected recall mode error")
	}
}

func TestLoadFromEnv_VectorEnabled(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_ADDR", "")
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_READ_ONLY", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_SESSION_IDLE_TTL", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_RECALL_MODE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_ENABLED", "true")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_TABLE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_DIMENSION", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_NAMESPACE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_INDEX", "")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if !cfg.VectorEnabled {
		t.Fatal("VectorEnabled = false, want true")
	}
}

func TestLoadFromEnv_VectorConfig(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_ADDR", "")
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_READ_ONLY", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_SESSION_IDLE_TTL", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_RECALL_MODE", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_ENABLED", "")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_TABLE", "gateway_vec")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_DIMENSION", "64")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_NAMESPACE", "memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_VECTOR_INDEX", "ivfflat")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_OUTBOX_POLL_INTERVAL", "2s")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_OUTBOX_BATCH_SIZE", "25")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.VectorTable != "gateway_vec" || cfg.VectorDimension != 64 || cfg.VectorNamespace != "memory" || cfg.VectorIndex != "ivfflat" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if cfg.OutboxPollInterval != 2*time.Second {
		t.Fatalf("OutboxPollInterval = %v, want 2s", cfg.OutboxPollInterval)
	}
	if cfg.OutboxBatchSize != 25 {
		t.Fatalf("OutboxBatchSize = %d, want 25", cfg.OutboxBatchSize)
	}
}

func TestLoadFromEnv_RejectsNonPositiveOutboxPollInterval(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_ADDR", "")
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_OUTBOX_POLL_INTERVAL", "0s")

	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("expected outbox poll interval error")
	}
}

func TestLoadFromEnv_RejectsNonPositiveOutboxBatchSize(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_ADDR", "")
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_OUTBOX_BATCH_SIZE", "0")

	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("expected outbox batch size error")
	}
}

func TestLoadFromEnv_EmbeddingCostMicrosPerTokenDefault(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_ADDR", "")
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_EMBED_COST_MICROS", "")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.EmbeddingCostMicrosPerToken != 0 {
		t.Fatalf("EmbeddingCostMicrosPerToken = %d, want 0 (default)", cfg.EmbeddingCostMicrosPerToken)
	}
}

func TestLoadFromEnv_EmbeddingCostMicrosPerTokenOverride(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_ADDR", "")
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_EMBED_COST_MICROS", "75")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.EmbeddingCostMicrosPerToken != 75 {
		t.Fatalf("EmbeddingCostMicrosPerToken = %d, want 75", cfg.EmbeddingCostMicrosPerToken)
	}
}

func TestLoadFromEnv_TraceSinkBufferSizeDefault(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_TRACE_BUFFER", "")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.TraceSinkBufferSize != 1024 {
		t.Fatalf("TraceSinkBufferSize = %d, want 1024 (default)", cfg.TraceSinkBufferSize)
	}
}

func TestLoadFromEnv_TraceSinkBufferSizeOverride(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_TRACE_BUFFER", "256")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.TraceSinkBufferSize != 256 {
		t.Fatalf("TraceSinkBufferSize = %d, want 256", cfg.TraceSinkBufferSize)
	}
}

func TestLoadFromEnv_TraceSinkBatchSizeDefault(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_TRACE_BATCH", "")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.TraceSinkBatchSize != 50 {
		t.Fatalf("TraceSinkBatchSize = %d, want 50 (default)", cfg.TraceSinkBatchSize)
	}
}

func TestLoadFromEnv_TraceSinkBatchSizeOverride(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_TRACE_BATCH", "20")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.TraceSinkBatchSize != 20 {
		t.Fatalf("TraceSinkBatchSize = %d, want 20", cfg.TraceSinkBatchSize)
	}
}

func TestLoadFromEnv_TraceSinkShutdownTimeoutDefault(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_TRACE_SHUTDOWN", "")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.TraceSinkShutdownTimeout != 5*time.Second {
		t.Fatalf("TraceSinkShutdownTimeout = %v, want 5s (default)", cfg.TraceSinkShutdownTimeout)
	}
}

func TestLoadFromEnv_TraceSinkShutdownTimeoutOverride(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_TRACE_SHUTDOWN", "2s")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.TraceSinkShutdownTimeout != 2*time.Second {
		t.Fatalf("TraceSinkShutdownTimeout = %v, want 2s", cfg.TraceSinkShutdownTimeout)
	}
}

func TestLoadFromEnv_StorageMetricsIntervalDefault(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_STORAGE_INTERVAL", "")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.StorageMetricsInterval != 5*time.Minute {
		t.Fatalf("StorageMetricsInterval = %v, want 5m (default)", cfg.StorageMetricsInterval)
	}
}

func TestLoadFromEnv_StorageMetricsIntervalOverride(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_STORAGE_INTERVAL", "30s")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.StorageMetricsInterval != 30*time.Second {
		t.Fatalf("StorageMetricsInterval = %v, want 30s", cfg.StorageMetricsInterval)
	}
}

func TestLoadFromEnv_TraceRetentionEnabledDefault(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_TRACE_RETENTION", "")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.TraceRetentionEnabled {
		t.Fatal("TraceRetentionEnabled = true, want false (default)")
	}
}

func TestLoadFromEnv_TraceRetentionEnabledOverride(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_TRACE_RETENTION", "true")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if !cfg.TraceRetentionEnabled {
		t.Fatal("TraceRetentionEnabled = false, want true")
	}
}

func TestLoadFromEnv_RelayLeaseTTLDefault(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_RELAY_LEASE_TTL", "")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.RelayLeaseTTL != 180*time.Second {
		t.Fatalf("RelayLeaseTTL = %v, want 180s (default)", cfg.RelayLeaseTTL)
	}
}

func TestLoadFromEnv_RelayLeaseTTLOverride(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_RELAY_LEASE_TTL", "45s")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.RelayLeaseTTL != 45*time.Second {
		t.Fatalf("RelayLeaseTTL = %v, want 45s", cfg.RelayLeaseTTL)
	}
}

func TestLoadFromEnv_RelayLeaseTTLRejectsNonPositive(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_RELAY_LEASE_TTL", "0s")

	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("expected error for non-positive relay lease ttl")
	}
}

func TestLoadFromEnv_RelayMaxAttemptsDefault(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_RELAY_MAX_ATTEMPTS", "")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.RelayMaxAttempts != 5 {
		t.Fatalf("RelayMaxAttempts = %d, want 5 (default)", cfg.RelayMaxAttempts)
	}
}

func TestLoadFromEnv_RelayMaxAttemptsOverride(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_RELAY_MAX_ATTEMPTS", "10")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.RelayMaxAttempts != 10 {
		t.Fatalf("RelayMaxAttempts = %d, want 10", cfg.RelayMaxAttempts)
	}
}

func TestLoadFromEnv_RelayBatchSizeDefault(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_RELAY_BATCH_SIZE", "")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.RelayBatchSize != 100 {
		t.Fatalf("RelayBatchSize = %d, want 100 (default)", cfg.RelayBatchSize)
	}
}

func TestLoadFromEnv_RelayBatchSizeOverride(t *testing.T) {
	t.Setenv("LLM_AGENT_MEMORY_PG_URL", "postgres://memory")
	t.Setenv("LLM_AGENT_MEMORY_GATEWAY_RELAY_BATCH_SIZE", "25")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.RelayBatchSize != 25 {
		t.Fatalf("RelayBatchSize = %d, want 25", cfg.RelayBatchSize)
	}
}
