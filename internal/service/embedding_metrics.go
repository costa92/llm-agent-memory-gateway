package service

import "strings"

// EmbeddingMetricsSink is the surface the embedding call sites write into. The
// observability.Metrics struct satisfies this interface; the indirection
// breaks the import cycle that would otherwise arise because
// internal/observability already imports internal/service. Call sites bucket
// the tenant_id via TenantBucket(...) before invoking these methods, keeping
// cardinality discipline in one place.
type EmbeddingMetricsSink interface {
	AddEmbeddingRequest(tenantBucket string)
	AddEmbeddingApplied(tenantBucket string)
	AddEmbeddingTokens(tenantBucket string, n uint64)
	AddEmbeddingCost(tenantBucket string, micros uint64)
}

// embeddingTokenCount is the gateway's local approximation of an embedding's
// token count. The rag/embed SDK does not return token counts at v1, so the
// gateway falls back to a whitespace-separated word count of the embedded
// text. This is intentionally a coarse proxy; operators set
// EmbeddingCostMicrosPerToken to calibrate the cost dimension against their
// own measured cost-per-input.
//
// Empty / whitespace-only text yields zero tokens; the call sites still emit
// the request and applied counters in that case, only the tokens and cost
// counters skip the increment.
func embeddingTokenCount(text string) uint64 {
	return uint64(len(strings.Fields(text)))
}
