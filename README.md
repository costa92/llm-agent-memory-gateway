[English](./README.md) | [简体中文](./README.zh-CN.md)

# llm-agent-memory-gateway

HTTP gateway and service-composition module for durable memory.

## Scope

This module owns:

- HTTP endpoints
- auth-derived tenant binding
- runtime configuration
- request validation and error mapping
- composition of SDK abstractions with backend implementations
- process startup

This module depends on:

- `github.com/costa92/llm-agent-memory`
- `github.com/costa92/llm-agent-memory-postgres`

## First-batch endpoints

- `POST /memory/recall/unified`
- `POST /memory/write`
- `PATCH /memory/items/{memory_id}`
- `POST /memory/items/{memory_id}/pin`
- `POST /memory/items/{memory_id}/unpin`
- `POST /memory/items/{memory_id}/disable`
- `POST /memory/items/{memory_id}/enable`
- `DELETE /memory/items/{memory_id}`
- `POST /memory/sessions/{session_id}/close`
- `POST /memory/sessions/{session_id}/heartbeat`
- `GET /metrics`

## Runtime configuration

- `LLM_AGENT_MEMORY_PG_URL` required
- `LLM_AGENT_MEMORY_GATEWAY_ADDR` optional, default `:8080`
- `LLM_AGENT_MEMORY_GATEWAY_READ_ONLY` optional, `true|false`
- `LLM_AGENT_MEMORY_GATEWAY_SESSION_IDLE_TTL` optional, default `30m`
- `LLM_AGENT_MEMORY_GATEWAY_RECALL_MODE` optional, `lexical|hybrid`, default `lexical`
- `LLM_AGENT_MEMORY_GATEWAY_VECTOR_ENABLED` optional, `true|false`, default `false`
- `LLM_AGENT_MEMORY_GATEWAY_VECTOR_TABLE` optional, default `memory_gateway_vectors`
- `LLM_AGENT_MEMORY_GATEWAY_VECTOR_DIMENSION` optional, default `32`
- `LLM_AGENT_MEMORY_GATEWAY_VECTOR_NAMESPACE` optional, default empty
- `LLM_AGENT_MEMORY_GATEWAY_VECTOR_INDEX` optional, `none|ivfflat|hnsw`, default `none`
- `LLM_AGENT_MEMORY_GATEWAY_OUTBOX_POLL_INTERVAL` optional, default `1s`
- `LLM_AGENT_MEMORY_GATEWAY_OUTBOX_BATCH_SIZE` optional, default `100` (legacy; superseded by `RELAY_BATCH_SIZE` below for the M8a-prep relay)
- `LLM_AGENT_MEMORY_GATEWAY_RELAY_LEASE_TTL` optional, default `180s`
- `LLM_AGENT_MEMORY_GATEWAY_RELAY_MAX_ATTEMPTS` optional, default `5`
- `LLM_AGENT_MEMORY_GATEWAY_RELAY_BATCH_SIZE` optional, default `100`

## Run

```bash
LLM_AGENT_MEMORY_PG_URL=postgres://... GOWORK=off go run ./cmd/memory-gateway
```

Startup now performs explicit gateway-owned schema migration for:

- `memory_gateway_session`
- `memory_gateway_scope_version`

You can also run the gateway-only migration command explicitly:

```bash
LLM_AGENT_MEMORY_PG_URL=postgres://... GOWORK=off go run ./cmd/memory-gateway-migrate
```

Auth scope for M6 is header-derived:

- `X-Tenant-Id`
- `X-User-Id`
- optional `X-Project-Id`
- optional `X-Session-Id`

Client JSON `scope` is accepted for shape parity, but the gateway always
overrides tenant/user scope with the auth-derived scope.

## Current recall behavior

The first M6 recall path now uses a pluggable hybrid recall seam:

- candidate generation is pluggable behind gateway-owned `RecallCandidateSource`
- truth-source hydration is pluggable behind gateway-owned `RecallRecordHydrator`
- default runtime wiring currently uses a Postgres lexical candidate source
  with simple `ILIKE` matching over content/category/tags
- `hybrid` mode is now wired at startup and includes a null vector candidate source placeholder
  so future vector backends can be attached without changing handler/service boundaries
- gateway now includes concrete runtime wiring for `llm-agent-rag/postgres` as a vector
  candidate source when `LLM_AGENT_MEMORY_GATEWAY_VECTOR_ENABLED=true`
- the current default embedder in that path is `llm-agent-rag/embed.HashEmbedder`
- vector candidates still only act as candidates; final visibility and returned payloads
  continue to be enforced through the gateway truth-source hydrator
- durable memory mutations write backend outbox events first, then a gateway-owned
  outbox relay worker projects those events into the configured vector store
- current outbox projection semantics are:
  - `write` / `patch` / `pin` / `unpin` / `enable` project as vector upsert
  - `disable` / `delete` project as vector remove
- before applying a projection event, the worker now re-reads the durable truth
  source and only applies the event when `event.version == current.version`
- outbox projection now emits structured observations for:
  - `projected`
  - `stale`
  - `failed`
  - `ignored`
- gateway now also exposes a minimal in-process metrics endpoint at `GET /metrics`
  with counters for current recall/outbox activity:
  - `recall_l1_hit_total` for eventual cache hits
  - `recall_l2_hit_total` for bounded cache hits
  - `recall_origin_total`
  - `recall_stale_served_total`
  - `recall_cache_fill_total`
  - `recall_invalidation_total`
  - `outbox_projection_projected_total`
  - `outbox_projection_stale_total`
  - `outbox_projection_failed_total`
  - `outbox_projection_ignored_total`
- request handlers no longer perform synchronous vector projection in the write path
- candidate hits are always re-hydrated from the durable truth source before
  they are returned
- DB-side scope filtering remains enforced from tenant/user/project/session
  inputs even if future vector candidate sources are added
- service-side candidate selection prefers memories that are:
  - pinned
  - `user_saved`
  - shorter / cheaper to fit into prompt budget
- gateway-owned L1 in-memory recall result cache is enabled
- consistency behavior currently is:
  - `eventual`: may reuse cached result and can serve stale cache when `allow_stale_cache=true`
  - `bounded`: only uses fresh L1 cache entries whose cached scope-version snapshot still matches the shared scope version token, and whose cached hit versions still match current truth-source object versions; otherwise it forces truth-source read
  - `strong`: bypasses recall cache and reads through to the truth source
- structured trace stages are emitted from the service layer
- token cost estimate is returned per hit
- concrete vector candidate source wiring and production recall quality are deferred

## Current session close behavior

`POST /memory/sessions/{session_id}/close` now enforces:

- auth-derived scope precedence
- explicit mode validation:
  - `expire_working`
  - `promote_and_expire`
- gateway-owned session lifecycle registry records closed sessions
- default runtime wiring persists session lifecycle state in Postgres
- default runtime wiring also persists shared scope-version tokens in Postgres for bounded-consistency invalidation
- recall against a closed session is rejected with `403`
- structured gateway trace emission

The gateway still does not own working-memory contents. It only owns session
lifecycle state, leaving local working data and cleanup mechanics to the agent
side.

`POST /memory/sessions/{session_id}/heartbeat` now:

- marks or refreshes an active session lifecycle record
- records active liveness separately from close state
- rejects heartbeats for already closed sessions with `403`
- rejects heartbeats for sessions idle past the configured session TTL with `403`
- keeps session lifecycle state gateway-owned without taking ownership of local
  working-memory payloads

Gateway session lifecycle now also enforces idle expiry:

- default idle TTL is `30m`
- idle TTL is refreshed by heartbeat
- recall against an idle-expired session is rejected with `403`

Design references:

- `docs/superpowers/specs/2026-05-26-memory-gateway-module-design.md`
- `docs/memory-gateway-api-contract.zh-CN.md`

## M7: Validation telemetry + decision trace

M7 adds best-effort persisted decision traces and a 10-counter validation
subset to the existing `/metrics` endpoint. Zero SDK changes; one Postgres
migration (the `memory_decision_trace` table, added by
`llm-agent-memory-postgres` Task 1). All counters carry a single
`tenant_bucket` label dimension (FNV-32a hash of `tenant_id` mod 32) to keep
metric cardinality bounded.

### New runtime configuration

| Env var | Default | Semantic |
|---|---|---|
| `LLM_AGENT_MEMORY_GATEWAY_EMBED_COST_MICROS` | `0` | Per-token cost (micro-units) applied to `embedding_cost_total`. Default 0 leaves the counter at zero. |
| `LLM_AGENT_MEMORY_GATEWAY_TRACE_BUFFER` | `1024` | In-memory queue size for the async decision-trace sink. Overflow drops bump `trace_dropped_total{reason="buffer_full"}`. |
| `LLM_AGENT_MEMORY_GATEWAY_TRACE_BATCH` | `50` | Max rows per `INSERT` issued by the sink writer. |
| `LLM_AGENT_MEMORY_GATEWAY_TRACE_SHUTDOWN` | `5s` | Drain budget on shutdown. Rows that miss the budget land in `trace_dropped_total{reason="shutdown"}`. |
| `LLM_AGENT_MEMORY_GATEWAY_STORAGE_INTERVAL` | `5m` | Period of the storage-bytes cron that refreshes `memory_storage_bytes_total` / `vector_storage_bytes_total`. |
| `LLM_AGENT_MEMORY_GATEWAY_TRACE_RETENTION` | `false` | Forward-looking flag (M8); v1 leaves trace retention as an operator obligation per spec OD-5. |

### Counters

| Name | Label | Source |
|---|---|---|
| `embedding_request_total` | `tenant_bucket` | Every Embed call site (success or error). |
| `embedding_applied_total` | `tenant_bucket` | Successful Embed calls only. |
| `embedding_tokens_total` | `tenant_bucket` | Token count returned by the embedder, summed per success. |
| `embedding_cost_total` | `tenant_bucket` | `tokens × LLM_AGENT_MEMORY_GATEWAY_EMBED_COST_MICROS`, summed per success. |
| `memory_storage_bytes_total` | `tenant_bucket` | Gauge — last write wins. Source: storage cron `SUM(octet_length(content))` over `memory_record`. |
| `vector_storage_bytes_total` | `tenant_bucket` | Gauge. **Zero in v1** — vector embeddings live in a separate RAG vector store (`llm-agent-rag/postgres`), not in this Postgres database. M8 may wire that source. |
| `episodic_disabled_total` | `tenant_bucket` | Outbox `memory_disabled` projection observations. |
| `episodic_deleted_total` | `tenant_bucket` | Outbox `memory_deleted` projection observations. |
| `recall_returned_total` | `tenant_bucket` | Count of records the recall backend returned (pre-budget). |
| `recall_selected_total` | `tenant_bucket` | Count of records that survived post-budget filtering and were returned. |
| `trace_dropped_total` | `reason` ∈ {`buffer_full`,`db_error`,`shutdown`} | Read straight from the sink's drop counters. The 3 reason values are bounded at compile time and the only legitimate non-`tenant_bucket` label in this set. |
| `storage_cron_failures_total` | _(none)_ | Global counter of failed storage-cron query ticks; the failure is not per-tenant. |

### Decision-trace persistence

The gateway persists structured decision-trace rows to the
`memory_decision_trace` table (migrated by `llm-agent-memory-postgres`).
Persistence is **best-effort**:

- The sink is non-blocking on the request path; rows that cannot be queued
  immediately are dropped and counted in `trace_dropped_total{reason="buffer_full"}`.
- The writer goroutine retries failed `INSERT`s with exponential backoff (3
  attempts, 50/200/800 ms); after the final attempt the batch is counted in
  `trace_dropped_total{reason="db_error"}`.
- On shutdown the writer drains the queue within `TRACE_SHUTDOWN`; remaining
  rows land in `trace_dropped_total{reason="shutdown"}`.

The `reason` column is free-form in v1; the enum is **frozen at M8** —
operators should treat it as opaque text for now and not gate alerts on
specific reason values until v2.

Tenant isolation is enforced two ways:

- The `tenant_id` column is written verbatim from the emitter so per-tenant
  trace queries stay clean.
- All counters bucket the tenant_id before storage. Two tenants in the same
  bucket share a metric value but neither tenant's identifier can be
  recovered from the counter — bucket aggregation is one-way.

## M8a-prep: Relay hardening + graceful shutdown

M8a-prep tightens the outbox relay's delivery semantics. Each row is now
claimed under a worker-owned lease (`claimed_by`, `claimed_at`,
`lease_expires_at`) so a crashed pod's work becomes reclaimable by a peer
after the lease TTL elapses — no operator action required.

### New runtime configuration

| Env var | Default | Semantic |
|---|---|---|
| `LLM_AGENT_MEMORY_GATEWAY_RELAY_LEASE_TTL` | `180s` | Per-claim lease window. A peer reclaims the row once `lease_expires_at < NOW()`. Tune down for faster failover (at the cost of more thrash if publish is slow). |
| `LLM_AGENT_MEMORY_GATEWAY_RELAY_MAX_ATTEMPTS` | `5` | Retry budget per row. After `attempt_count >= MaxAttempts`, the row transitions to `failed` and waits for `RequeueFailed`. |
| `LLM_AGENT_MEMORY_GATEWAY_RELAY_BATCH_SIZE` | `100` | Maximum rows per `ClaimBatch`. Supersedes `OUTBOX_BATCH_SIZE` for the M8a-prep relay. |

### Deployment topology

The relay supports graceful shutdown by calling `Relay.Release(ctx)` from
the gateway's cleanup sequence (before `pool.Close`). Release flips every
row currently held by this worker's `claimed_by` back to `pending` so a
peer can immediately re-claim without waiting for the lease TTL.

For Kubernetes deployments:

- Set `terminationGracePeriodSeconds >= RelayLeaseTTL + 10s`. The +10s
  is slack for the in-flight publish + the synchronous Release Exec; the
  default lease TTL of 180s implies a minimum grace period of ~190s.
- A `preStop` hook is not strictly required — the gateway's cleanup
  registers Release at startup and runs it during normal SIGTERM
  shutdown. Use preStop only if your platform delivers SIGKILL on
  termination instead of SIGTERM.
- Multiple replicas are safe: each pod generates a fresh
  `<hostname>-<128-bit-hex>` worker identity at startup, so leases are
  always pod-scoped. A pod that crashes mid-publish leaves its leases
  in place until the TTL expires; a peer reclaims them at the next
  `ClaimBatch`.

### Operator API

For rows that exhaust `RelayMaxAttempts` and land in `failed`:

- `Store.ListFailed(ctx, limit)` — newest-first survey of failed rows
  (outbox_id, aggregate_id, event_type, attempt_count, last_error,
  created_at).
- `Store.RequeueFailed(ctx, outboxID)` — flips a `failed` row back to
  `pending` and resets `attempt_count` to 0. No-op on non-`failed`
  rows; reports `RowsAffected` so operator tooling can detect typos.

Postgres 11+ is required for the M8a-prep migration (nullable `ADD
COLUMN` must be metadata-only).
