# Changelog

All notable changes to `github.com/costa92/llm-agent-memory-gateway` will be
documented in this file.

<!-- Keep a Changelog format: https://keepachangelog.com/en/1.1.0/ -->
<!-- Semver: https://semver.org/ -->

## [Unreleased]

### Added

- M8a-prep relay wiring + event dispatch:
  - Three new env vars threaded through `RelayConfig`:
    - `LLM_AGENT_MEMORY_GATEWAY_RELAY_LEASE_TTL` (default `180s`)
    - `LLM_AGENT_MEMORY_GATEWAY_RELAY_MAX_ATTEMPTS` (default `5`)
    - `LLM_AGENT_MEMORY_GATEWAY_RELAY_BATCH_SIZE` (default `100`,
      supersedes the legacy `OUTBOX_BATCH_SIZE` for the M8a-prep relay)
  - `Relay.Release(ctx)` is now registered in the gateway shutdown
    sequence **before** `pool.Close`, so an in-flight relay tick has its
    claimed rows flipped back to `pending` instead of waiting out the
    full lease TTL when the gateway terminates.
  - Two new case arms in `OutboxVectorPublisher`:
    - `memory_promoted` — observation-only (`status="promoted_noop"`);
      no vector mutation. The underlying record was already projected
      at `memory_created` time.
    - `memory_dedupe_collapsed` — observation-only
      (`status="dedupe_collapsed_observed"`). Loser cleanup is
      emitted as a matching `memory_deleted` event in the same M8a
      transaction; this arm records the collapse fact.
  - Deployment topology guidance added to README: recommended
    `terminationGracePeriodSeconds >= RelayLeaseTTL + ~10s` slack.
- M7 validation telemetry and decision-trace persistence:
  - Best-effort async `PostgresDecisionTraceSink` that batch-inserts decision
    traces into `memory_decision_trace` (table migrated by
    `llm-agent-memory-postgres`).
  - The sink is composed into the existing `TraceEmitter` chain through a
    new `observability.TraceSinkEmitter` adapter, so every existing
    `traceEmitter.Emit` call site forwards to the sink without changing the
    call-site shape.
  - 10 validation counters on `GET /metrics`, all carrying a single
    `tenant_bucket` label (FNV-32a `tenant_id` mod 32) to keep cardinality
    bounded:
    - `embedding_request_total`, `embedding_applied_total`,
      `embedding_tokens_total`, `embedding_cost_total`
    - `memory_storage_bytes_total`, `vector_storage_bytes_total`
    - `episodic_disabled_total`, `episodic_deleted_total`
    - `recall_returned_total`, `recall_selected_total`
  - `trace_dropped_total{reason}` (3 values: `buffer_full`, `db_error`,
    `shutdown`), read straight from the sink's drop counters.
  - `storage_cron_failures_total` (operational counter for the storage-bytes
    cron; no per-tenant label).
- `StorageMetricsCron` — periodic per-tenant storage-bytes snapshot. Default
  interval 5 minutes; configurable via
  `LLM_AGENT_MEMORY_GATEWAY_STORAGE_INTERVAL`.
- 6 new runtime config knobs:
  - `LLM_AGENT_MEMORY_GATEWAY_EMBED_COST_MICROS` (default `0`)
  - `LLM_AGENT_MEMORY_GATEWAY_TRACE_BUFFER` (default `1024`)
  - `LLM_AGENT_MEMORY_GATEWAY_TRACE_BATCH` (default `50`)
  - `LLM_AGENT_MEMORY_GATEWAY_TRACE_SHUTDOWN` (default `5s`)
  - `LLM_AGENT_MEMORY_GATEWAY_STORAGE_INTERVAL` (default `5m`)
  - `LLM_AGENT_MEMORY_GATEWAY_TRACE_RETENTION` (default `false`; reserved
    for M8 — v1 treats trace retention as an operator obligation per spec
    OD-5).

### Notes

- `vector_storage_bytes_total` is reported as **0** in v1 because vector
  embeddings live in a separate RAG vector store (the `llm-agent-rag/postgres`
  backend), not in this Postgres database. The counter shape stays stable
  so M8 can wire the second source without breaking dashboards.
- The `memory_decision_trace.reason` column is free-form in v1; the enum
  is frozen at M8. Operators should treat it as opaque text for now.
- Decision-trace persistence is best-effort with bounded loss accounting
  through `trace_dropped_total`. The request path never blocks on the sink.
- No SDK changes; no new event types; no new sibling modules.

## [0.4.0] - 2026-06-02

### Added

- **Hard-delete GC (M8 D4).** A background `HardDeleteGCCron` physically
  removes `memory_record` rows that have been soft-deleted (`deleted=TRUE`) for
  longer than the retention window, reclaiming storage. Soft-deleted rows are
  already invisible to every query, so this changes no behaviour;
  `memory_event` / outbox rows have no FK to `memory_record` and are left as
  history. New global counter `memory_hard_deleted_total`. **Off by default**
  (irreversible physical delete). Config:
  `LLM_AGENT_MEMORY_GATEWAY_HARD_DELETE_GC_ENABLED` (default false),
  `..._HARD_DELETE_RETENTION` (default 30d), `..._HARD_DELETE_GC_INTERVAL`
  (default 1h).

## [0.3.1] - 2026-06-02

### Fixed

- Bumped `llm-agent-memory-postgres` to `v0.1.1`, pulling in the `ResolveDedupe`
  first-writer race fix (C1). The wired session closer and reaper both go
  through `ResolveDedupe`, so this is required for the fix to be in the gateway
  binary (it previously pinned the buggy `v0.1.0`).

## [0.3.0] - 2026-06-02

### Added

- **Orphaned-session reaper (M8 D6).** A background `SessionReaperCron`
  periodically reclaims the working memory of sessions that went idle past
  `SessionIdleTTL` but were never explicitly closed — keyed off the working
  records (not session-state, which is only written by the heartbeat/close
  endpoints). Config: `LLM_AGENT_MEMORY_GATEWAY_SESSION_REAPER_ENABLED`
  (default true) and `..._SESSION_REAPER_INTERVAL` (default 5m).

## [0.2.0] - 2026-06-02

### Added

- **Working-memory session lifecycle enablement (M8).** Wired the
  `DurableSessionCloser` in production (was a no-op): `POST /sessions/{id}/close`
  now expires/promotes the session's working records. New tenant-bucketed
  metrics `working_expired_total`, `working_dropped_before_use_total`,
  `working_promoted_total`.

### Changed

- Promotion eligibility / threshold / dedupe-key now come from
  `llm-agent-memory-contract` `v0.2.0` instead of gateway-local copies (D3).
- **Write-API semantic change (D8):** `WriteMemory` / `PatchMemory` /
  `PinMemory` / `DisableMemory` / `DeleteMemory` now return `403` for a closed
  session (previously silent success), matching the read path. Sessionless
  writes are unaffected.

## [0.1.0] - 2026-05-26

### Added

- Initial HTTP gateway and service-composition module split out from the SDK,
  fronting durable memory:
  - HTTP API surface for memory write/recall and operator endpoints.
  - service composition wiring the Postgres backend, RAG vector store, and
    relay together.
  - `GET /metrics` and runtime configuration via `LLM_AGENT_MEMORY_GATEWAY_*`
    environment variables.

### Dependencies

- `github.com/costa92/llm-agent-memory` for SDK-owned durable abstractions
- `github.com/costa92/llm-agent-memory-postgres` for the durable backend + relay
- `github.com/costa92/llm-agent-memory-contract` for backend-neutral contract types
- `github.com/costa92/llm-agent-rag` for the vector store backing recall

### Notes

- Gateway HTTP and service composition are intentionally separate from the SDK,
  Postgres, and worker modules.
