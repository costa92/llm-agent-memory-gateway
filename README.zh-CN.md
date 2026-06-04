[English](./README.md) | [简体中文](./README.zh-CN.md)

# llm-agent-memory-gateway

面向持久记忆的 HTTP 网关与服务编排模块。

## 范围

本模块负责：

- HTTP 端点
- 由 auth 派生的租户绑定
- 运行时配置
- 请求校验与错误映射
- 将 SDK 抽象与后端实现进行编排组合
- 进程启动

本模块依赖：

- `github.com/costa92/llm-agent-memory`
- `github.com/costa92/llm-agent-memory-postgres`

## 首批端点

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

## 运行时配置

- `LLM_AGENT_MEMORY_PG_URL` 必填
- `LLM_AGENT_MEMORY_GATEWAY_ADDR` 可选，默认 `:8080`
- `LLM_AGENT_MEMORY_GATEWAY_READ_ONLY` 可选，`true|false`
- `LLM_AGENT_MEMORY_GATEWAY_SESSION_IDLE_TTL` 可选，默认 `30m`
- `LLM_AGENT_MEMORY_GATEWAY_RECALL_MODE` 可选，`lexical|hybrid`，默认 `lexical`
- `LLM_AGENT_MEMORY_GATEWAY_VECTOR_ENABLED` 可选，`true|false`，默认 `false`
- `LLM_AGENT_MEMORY_GATEWAY_VECTOR_TABLE` 可选，默认 `memory_gateway_vectors`
- `LLM_AGENT_MEMORY_GATEWAY_VECTOR_DIMENSION` 可选，默认 `32`
- `LLM_AGENT_MEMORY_GATEWAY_VECTOR_NAMESPACE` 可选，默认为空
- `LLM_AGENT_MEMORY_GATEWAY_VECTOR_INDEX` 可选，`none|ivfflat|hnsw`，默认 `none`
- `LLM_AGENT_MEMORY_GATEWAY_OUTBOX_POLL_INTERVAL` 可选，默认 `1s`
- `LLM_AGENT_MEMORY_GATEWAY_OUTBOX_BATCH_SIZE` 可选，默认 `100`（遗留项；下文 M8a-prep 中继已用 `RELAY_BATCH_SIZE` 取代它）
- `LLM_AGENT_MEMORY_GATEWAY_RELAY_LEASE_TTL` 可选，默认 `180s`
- `LLM_AGENT_MEMORY_GATEWAY_RELAY_MAX_ATTEMPTS` 可选，默认 `5`
- `LLM_AGENT_MEMORY_GATEWAY_RELAY_BATCH_SIZE` 可选，默认 `100`

## 运行

```bash
LLM_AGENT_MEMORY_PG_URL=postgres://... GOWORK=off go run ./cmd/memory-gateway
```

启动时现在会对以下表执行由网关自身负责的 schema 迁移：

- `memory_gateway_session`
- `memory_gateway_scope_version`

你也可以显式运行仅限网关的迁移命令：

```bash
LLM_AGENT_MEMORY_PG_URL=postgres://... GOWORK=off go run ./cmd/memory-gateway-migrate
```

M6 的 auth 作用域由请求头派生：

- `X-Tenant-Id`
- `X-User-Id`
- 可选 `X-Project-Id`
- 可选 `X-Session-Id`

客户端 JSON 中的 `scope` 会被接受以保持结构一致，但网关始终用由 auth
派生的作用域覆盖 tenant/user 作用域。

## 当前唤回行为

首个 M6 唤回路径现在使用一个可插拔的混合唤回接缝：

- 候选生成可通过网关自有的 `RecallCandidateSource` 进行插拔
- 真值源水合（truth-source hydration）可通过网关自有的 `RecallRecordHydrator` 进行插拔
- 默认运行时接线目前使用 Postgres 词法候选源，
  通过对 content/category/tags 做简单的 `ILIKE` 匹配
- `hybrid` 模式现已在启动时接线，并包含一个空向量候选源占位符，
  以便未来的向量后端可以在不改变 handler/service 边界的前提下接入
- 网关现在为 `llm-agent-rag/postgres` 提供了具体的运行时接线，在
  `LLM_AGENT_MEMORY_GATEWAY_VECTOR_ENABLED=true` 时作为向量候选源
- 该路径中当前默认的嵌入器是 `llm-agent-rag/embed.HashEmbedder`
- 向量候选仍然仅作为候选；最终的可见性与返回的载荷
  仍通过网关真值源水合器强制执行
- 持久记忆的写改操作会先写入后端发件箱（outbox）事件，然后由网关自有的
  发件箱中继工作进程将这些事件投影到所配置的向量存储中
- 当前发件箱投影语义为：
  - `write` / `patch` / `pin` / `unpin` / `enable` 投影为向量 upsert（写入或更新）
  - `disable` / `delete` 投影为向量移除
- 在应用一个投影事件之前，工作进程现在会重新读取持久真值源，
  仅当 `event.version == current.version` 时才应用该事件
- 发件箱投影现在会为以下情况发出结构化观察：
  - `projected`
  - `stale`
  - `failed`
  - `ignored`
- 网关现在还在 `GET /metrics` 暴露一个最小化的进程内指标端点，
  包含当前唤回/发件箱活动的计数器：
  - `recall_l1_hit_total` 表示 eventual 缓存命中
  - `recall_l2_hit_total` 表示 bounded 缓存命中
  - `recall_origin_total`
  - `recall_stale_served_total`
  - `recall_cache_fill_total`
  - `recall_invalidation_total`
  - `outbox_projection_projected_total`
  - `outbox_projection_stale_total`
  - `outbox_projection_failed_total`
  - `outbox_projection_ignored_total`
- 请求 handler 不再在写路径中执行同步向量投影
- 候选命中在返回之前始终会从持久真值源重新水合
- 即便未来加入向量候选源，DB 侧的作用域过滤仍会基于
  tenant/user/project/session 输入强制执行
- service 侧的候选选择会优先选择满足以下条件的记忆：
  - 已钉住
  - `user_saved`
  - 更短 / 更省成本以便装入 prompt 预算
- 网关自有的 L1 进程内唤回结果缓存已启用
- 一致性行为目前为：
  - `eventual`：可复用缓存结果，并在 `allow_stale_cache=true` 时可返回陈旧缓存
  - `bounded`：仅使用其缓存的 scope-version 快照仍与共享 scope 版本令牌匹配、
    且其缓存命中版本仍与当前真值源对象版本匹配的新鲜 L1 缓存条目；否则强制做真值源读取
  - `strong`：绕过唤回缓存，直接读穿到真值源
- 结构化链路阶段从 service 层发出
- 每次命中都会返回 token 成本估算
- 具体向量候选源接线与生产级唤回质量被推迟

## 当前会话关闭行为

`POST /memory/sessions/{session_id}/close` 现在强制执行：

- 由 auth 派生的作用域优先级
- 显式的模式校验：
  - `expire_working`
  - `promote_and_expire`
- 网关自有的会话生命周期注册表记录已关闭的会话
- 默认运行时接线将会话生命周期状态持久化到 Postgres
- 默认运行时接线还会将共享 scope-version 令牌持久化到 Postgres，用于 bounded 一致性失效
- 针对已关闭会话的唤回会以 `403` 拒绝
- 结构化网关链路发出

网关仍不拥有工作记忆内容。它只拥有会话
生命周期状态，将本地工作数据和清理机制留给智能体
一侧。

`POST /memory/sessions/{session_id}/heartbeat` 现在会：

- 标记或刷新一条活跃的会话生命周期记录
- 将活跃存活状态与关闭状态分开记录
- 对已关闭会话的心跳以 `403` 拒绝
- 对空闲时间超过所配置会话 TTL 的会话的心跳以 `403` 拒绝
- 在不接管本地工作记忆载荷所有权的前提下，使会话生命周期状态保持网关自有

网关会话生命周期现在还会强制执行空闲过期：

- 默认空闲 TTL 为 `30m`
- 空闲 TTL 由心跳刷新
- 针对已空闲过期会话的唤回会以 `403` 拒绝

设计参考：

- `docs/superpowers/specs/2026-05-26-memory-gateway-module-design.md`
- `docs/memory-gateway-api-contract.zh-CN.md`

## M7：校验遥测 + 决策链路

M7 在现有的 `/metrics` 端点上新增了尽力而为的持久化决策链路以及一个 10 计数器的校验
子集。零 SDK 改动；一次 Postgres
迁移（`memory_decision_trace` 表，由
`llm-agent-memory-postgres` Task 1 添加）。所有计数器都带有单一的
`tenant_bucket` 标签维度（`tenant_id` 的 FNV-32a 哈希对 32 取模），以保持
指标基数有界。

### 新增运行时配置

| Env var | Default | Semantic |
|---|---|---|
| `LLM_AGENT_MEMORY_GATEWAY_EMBED_COST_MICROS` | `0` | 每 token 成本（微单位），应用于 `embedding_cost_total`。默认 0 使该计数器保持为零。 |
| `LLM_AGENT_MEMORY_GATEWAY_TRACE_BUFFER` | `1024` | 异步决策链路落地端的进程内队列大小。溢出丢弃会使 `trace_dropped_total{reason="buffer_full"}` 递增。 |
| `LLM_AGENT_MEMORY_GATEWAY_TRACE_BATCH` | `50` | 落地写入器每次 `INSERT` 的最大行数。 |
| `LLM_AGENT_MEMORY_GATEWAY_TRACE_SHUTDOWN` | `5s` | 关闭时的排空预算。错过该预算的行会落入 `trace_dropped_total{reason="shutdown"}`。 |
| `LLM_AGENT_MEMORY_GATEWAY_STORAGE_INTERVAL` | `5m` | 刷新 `memory_storage_bytes_total` / `vector_storage_bytes_total` 的存储字节 cron 周期。 |
| `LLM_AGENT_MEMORY_GATEWAY_TRACE_RETENTION` | `false` | 前瞻性标志（M8）；v1 按 spec OD-5 将链路保留留作运维责任。 |

### 计数器

| Name | Label | Source |
|---|---|---|
| `embedding_request_total` | `tenant_bucket` | 每个 Embed 调用点（成功或出错）。 |
| `embedding_applied_total` | `tenant_bucket` | 仅成功的 Embed 调用。 |
| `embedding_tokens_total` | `tenant_bucket` | 嵌入器返回的 token 数，按每次成功累加。 |
| `embedding_cost_total` | `tenant_bucket` | `tokens × LLM_AGENT_MEMORY_GATEWAY_EMBED_COST_MICROS`，按每次成功累加。 |
| `memory_storage_bytes_total` | `tenant_bucket` | 测量值（Gauge）——最后一次写入获胜。来源：存储 cron 对 `memory_record` 的 `SUM(octet_length(content))`。 |
| `vector_storage_bytes_total` | `tenant_bucket` | 测量值（Gauge）。**v1 中为零**——向量嵌入存放在独立的 RAG 向量存储（`llm-agent-rag/postgres`）中，而非本 Postgres 数据库。M8 可能会接线该来源。 |
| `episodic_disabled_total` | `tenant_bucket` | 发件箱 `memory_disabled` 投影观察。 |
| `episodic_deleted_total` | `tenant_bucket` | 发件箱 `memory_deleted` 投影观察。 |
| `recall_returned_total` | `tenant_bucket` | 唤回后端返回的记录数（预算前）。 |
| `recall_selected_total` | `tenant_bucket` | 通过预算后过滤并被返回的记录数。 |
| `trace_dropped_total` | `reason` ∈ {`buffer_full`,`db_error`,`shutdown`} | 直接读取落地端的丢弃计数器。这 3 个 reason 值在编译期就有界，是本组中唯一合法的非 `tenant_bucket` 标签。 |
| `storage_cron_failures_total` | _(none)_ | 失败的存储 cron 查询周期的全局计数器；该失败不是按租户的。 |

### 决策链路持久化

网关将结构化的决策链路行持久化到
`memory_decision_trace` 表（由 `llm-agent-memory-postgres` 迁移）。
持久化是**尽力而为**的：

- 落地端在请求路径上是非阻塞的；无法立即入队的行
  会被丢弃，并计入 `trace_dropped_total{reason="buffer_full"}`。
- 写入器 goroutine 会以指数退避重试失败的 `INSERT`（3
  次尝试，50/200/800 ms）；在最后一次尝试之后，该批次会计入
  `trace_dropped_total{reason="db_error"}`。
- 关闭时，写入器会在 `TRACE_SHUTDOWN` 之内排空队列；剩余的
  行会落入 `trace_dropped_total{reason="shutdown"}`。

`reason` 列在 v1 中是自由格式的；该枚举**在 M8 冻结**——
运维人员目前应将其视为不透明文本，在 v2 之前不要基于
特定 reason 值设置告警。

租户隔离通过两种方式强制执行：

- `tenant_id` 列由发出方逐字写入，使按租户的
  链路查询保持清晰。
- 所有计数器在存储前都会对 tenant_id 分桶。同一桶中的两个租户
  共享一个指标值，但无法从计数器中恢复任一租户的标识符——
  分桶聚合是单向的。

## M8a-prep：中继加固 + 优雅关闭

M8a-prep 收紧了发件箱中继的投递语义。每一行现在都
在一个工作进程自有的租约（`claimed_by`、`claimed_at`、
`lease_expires_at`）下被认领，因此在租约 TTL 过去之后，已崩溃 pod 的工作
会变得可被对等节点重新认领——无需运维操作。

### 新增运行时配置

| Env var | Default | Semantic |
|---|---|---|
| `LLM_AGENT_MEMORY_GATEWAY_RELAY_LEASE_TTL` | `180s` | 每次认领的租约窗口。一旦 `lease_expires_at < NOW()`，对等节点即可重新认领该行。调小可加快故障转移（代价是发布慢时会有更多抖动）。 |
| `LLM_AGENT_MEMORY_GATEWAY_RELAY_MAX_ATTEMPTS` | `5` | 每行的重试预算。当 `attempt_count >= MaxAttempts` 后，该行转为 `failed` 并等待 `RequeueFailed`。 |
| `LLM_AGENT_MEMORY_GATEWAY_RELAY_BATCH_SIZE` | `100` | 每次 `ClaimBatch` 的最大行数。在 M8a-prep 中继中取代 `OUTBOX_BATCH_SIZE`。 |

### 部署拓扑

中继通过在网关清理序列中（在 `pool.Close` 之前）调用
`Relay.Release(ctx)` 来支持优雅关闭。Release 会把当前由本工作进程
`claimed_by` 持有的每一行翻回 `pending`，以便
对等节点可以立即重新认领，无需等待租约 TTL。

对于 Kubernetes 部署：

- 设置 `terminationGracePeriodSeconds >= RelayLeaseTTL + 10s`。这 +10s
  是为在途发布 + 同步 Release Exec 预留的余量；默认租约 TTL 180s 意味着最小宽限期约为 ~190s。
- 严格来说并不需要 `preStop` 钩子——网关的清理在启动时
  注册 Release，并在正常的 SIGTERM 关闭期间运行它。
  仅当你的平台在终止时投递 SIGKILL 而非 SIGTERM 时才使用 preStop。
- 多副本是安全的：每个 pod 在启动时生成一个全新的
  `<hostname>-<128-bit-hex>` 工作进程身份，因此租约
  始终是 pod 范围的。在发布途中崩溃的 pod 会让它的租约
  保持原状直到 TTL 过期；对等节点会在下一次
  `ClaimBatch` 时重新认领它们。

### 运维 API

对于耗尽 `RelayMaxAttempts` 并落入 `failed` 的行：

- `Store.ListFailed(ctx, limit)` —— 对失败行的最新优先巡览
  （outbox_id、aggregate_id、event_type、attempt_count、last_error、
  created_at）。
- `Store.RequeueFailed(ctx, outboxID)` —— 把一个 `failed` 行翻回
  `pending` 并将 `attempt_count` 重置为 0。对非 `failed`
  行为空操作；返回 `RowsAffected` 以便运维工具检测拼写错误。

M8a-prep 迁移要求 Postgres 11+（可空的 `ADD
COLUMN` 必须是仅元数据的）。
