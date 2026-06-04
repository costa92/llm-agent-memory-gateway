# Changelog

`github.com/costa92/llm-agent-memory-gateway` 的所有重要变更都将
记录在本文件中。

<!-- Keep a Changelog format: https://keepachangelog.com/en/1.1.0/ -->
<!-- Semver: https://semver.org/ -->

## [Unreleased]

### Added

- M8a-prep 中继接线 + 事件分发：
  - 三个新的 env var 贯通到 `RelayConfig`：
    - `LLM_AGENT_MEMORY_GATEWAY_RELAY_LEASE_TTL`（默认 `180s`）
    - `LLM_AGENT_MEMORY_GATEWAY_RELAY_MAX_ATTEMPTS`（默认 `5`）
    - `LLM_AGENT_MEMORY_GATEWAY_RELAY_BATCH_SIZE`（默认 `100`，
      在 M8a-prep 中继中取代遗留的 `OUTBOX_BATCH_SIZE`）
  - `Relay.Release(ctx)` 现在在网关关闭序列中**于** `pool.Close`
    **之前**注册，因此在途的中继 tick 会在网关终止时把其
    已认领的行翻回 `pending`，而不是耗到
    完整的租约 TTL。
  - `OutboxVectorPublisher` 中的两个新 case 分支：
    - `memory_promoted` —— 仅观察（`status="promoted_noop"`）；
      无向量改动。底层记录在 `memory_created` 时
      就已经投影过了。
    - `memory_dedupe_collapsed` —— 仅观察
      （`status="dedupe_collapsed_observed"`）。败者清理会
      在同一 M8a 事务中作为一个匹配的 `memory_deleted` 事件
      发出；此分支记录该折叠事实。
  - README 中新增了部署拓扑指引：建议
    `terminationGracePeriodSeconds >= RelayLeaseTTL + ~10s` 余量。
- M7 校验遥测与决策链路持久化：
  - 尽力而为的异步 `PostgresDecisionTraceSink`，将决策
    链路批量插入到 `memory_decision_trace`（表由
    `llm-agent-memory-postgres` 迁移）。
  - 该落地端通过一个新的 `observability.TraceSinkEmitter` 适配器
    组合进现有的 `TraceEmitter` 链中，因此每个现有的
    `traceEmitter.Emit` 调用点都会在不改变调用点
    形态的前提下转发到落地端。
  - `GET /metrics` 上的 10 个校验计数器，均带有单一的
    `tenant_bucket` 标签（`tenant_id` 的 FNV-32a 对 32 取模），以保持基数
    有界：
    - `embedding_request_total`、`embedding_applied_total`、
      `embedding_tokens_total`、`embedding_cost_total`
    - `memory_storage_bytes_total`、`vector_storage_bytes_total`
    - `episodic_disabled_total`、`episodic_deleted_total`
    - `recall_returned_total`、`recall_selected_total`
  - `trace_dropped_total{reason}`（3 个值：`buffer_full`、`db_error`、
    `shutdown`），直接读取落地端的丢弃计数器。
  - `storage_cron_failures_total`（存储字节 cron 的运维计数器；
    无按租户标签）。
- `StorageMetricsCron` —— 周期性的按租户存储字节快照。默认
  间隔 5 分钟；可通过
  `LLM_AGENT_MEMORY_GATEWAY_STORAGE_INTERVAL` 配置。
- 6 个新的运行时配置旋钮：
  - `LLM_AGENT_MEMORY_GATEWAY_EMBED_COST_MICROS`（默认 `0`）
  - `LLM_AGENT_MEMORY_GATEWAY_TRACE_BUFFER`（默认 `1024`）
  - `LLM_AGENT_MEMORY_GATEWAY_TRACE_BATCH`（默认 `50`）
  - `LLM_AGENT_MEMORY_GATEWAY_TRACE_SHUTDOWN`（默认 `5s`）
  - `LLM_AGENT_MEMORY_GATEWAY_STORAGE_INTERVAL`（默认 `5m`）
  - `LLM_AGENT_MEMORY_GATEWAY_TRACE_RETENTION`（默认 `false`；为 M8
    保留——v1 按 spec OD-5 将链路保留视为运维
    责任）。

### Notes

- `vector_storage_bytes_total` 在 v1 中报告为 **0**，因为向量
  嵌入存放在独立的 RAG 向量存储（`llm-agent-rag/postgres`
  后端）中，而非本 Postgres 数据库。计数器形态保持稳定，
  以便 M8 可以在不破坏仪表盘的情况下接入第二个来源。
- `memory_decision_trace.reason` 列在 v1 中是自由格式的；该枚举
  在 M8 冻结。运维人员目前应将其视为不透明文本。
- 决策链路持久化是尽力而为的，并通过 `trace_dropped_total`
  做有界的丢失计量。请求路径从不阻塞在落地端上。
- 无 SDK 改动；无新事件类型；无新兄弟模块。

## [0.4.0] - 2026-06-02

### Added

- **硬删除 GC（M8 D4）。** 后台 `HardDeleteGCCron` 会物理
  移除已被软删除（`deleted=TRUE`）超过保留窗口的 `memory_record`
  行，回收存储。软删除的行
  已对每个查询不可见，因此这不改变任何行为；
  `memory_event` / 发件箱行与 `memory_record` 没有 FK，会作为
  历史保留。新增全局计数器 `memory_hard_deleted_total`。**默认关闭**
  （不可逆的物理删除）。配置：
  `LLM_AGENT_MEMORY_GATEWAY_HARD_DELETE_GC_ENABLED`（默认 false）、
  `..._HARD_DELETE_RETENTION`（默认 30d）、`..._HARD_DELETE_GC_INTERVAL`
  （默认 1h）。

## [0.3.1] - 2026-06-02

### Fixed

- 将 `llm-agent-memory-postgres` 版本提升到 `v0.1.1`，引入 `ResolveDedupe`
  首写者竞态修复（C1）。已接线的会话关闭器与回收器都会
  经过 `ResolveDedupe`，因此该修复要进入网关
  二进制就需要这次提升（它此前锚定的是有缺陷的 `v0.1.0`）。

## [0.3.0] - 2026-06-02

### Added

- **孤儿会话回收器（M8 D6）。** 后台 `SessionReaperCron`
  会周期性回收那些空闲超过 `SessionIdleTTL` 但从未被显式关闭的
  会话的工作记忆——以工作
  记录为键（而非会话状态，后者仅由 heartbeat/close
  端点写入）。配置：`LLM_AGENT_MEMORY_GATEWAY_SESSION_REAPER_ENABLED`
  （默认 true）与 `..._SESSION_REAPER_INTERVAL`（默认 5m）。

## [0.2.0] - 2026-06-02

### Added

- **工作记忆会话生命周期启用（M8）。** 在生产中接线了
  `DurableSessionCloser`（此前是空操作）：`POST /sessions/{id}/close`
  现在会过期/晋升会话的工作记录。新增按租户分桶的
  指标 `working_expired_total`、`working_dropped_before_use_total`、
  `working_promoted_total`。

### Changed

- 晋升资格 / 阈值 / dedupe-key 现在来自
  `llm-agent-memory-contract` `v0.2.0`，而非网关本地副本（D3）。
- **写 API 语义变更（D8）：** `WriteMemory` / `PatchMemory` /
  `PinMemory` / `DisableMemory` / `DeleteMemory` 现在对已关闭
  会话返回 `403`（此前是静默成功），与读路径一致。无会话的
  写不受影响。

## [0.1.0] - 2026-05-26

### Added

- 从 SDK 拆分出的初始 HTTP 网关与服务编排模块，
  作为持久记忆的前端：
  - 用于记忆 write/recall 与运维端点的 HTTP API 面。
  - 将 Postgres 后端、RAG 向量存储与
    中继接线在一起的服务编排。
  - `GET /metrics` 以及通过 `LLM_AGENT_MEMORY_GATEWAY_*`
    环境变量进行的运行时配置。

### Dependencies

- `github.com/costa92/llm-agent-memory` 提供 SDK 自有的持久抽象
- `github.com/costa92/llm-agent-memory-postgres` 提供持久后端 + 中继
- `github.com/costa92/llm-agent-memory-contract` 提供后端中立的契约类型
- `github.com/costa92/llm-agent-rag` 提供支撑唤回的向量存储

### Notes

- 网关 HTTP 与服务编排被有意与 SDK、
  Postgres 和 worker 模块分开。
