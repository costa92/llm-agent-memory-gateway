package service

import (
	"fmt"
	"hash/fnv"
)

// TenantBucketModulus is the number of buckets used by tenantBucket. It is
// exported so other packages (and docs/tests) can reference the same constant
// when reasoning about counter cardinality.
const TenantBucketModulus = 32

// tenantBucket hashes a tenant_id into one of TenantBucketModulus stable
// buckets, formatted as a two-digit decimal string ("00".."31"). Empty input
// returns "unknown" so callers do not need to guard.
//
// Bucketing keeps per-tenant counters from blowing up Prometheus cardinality
// while still making cross-tenant trends visible. The mapping is intentionally
// one-way: two tenants in the same bucket are indistinguishable from the
// metrics surface.
func tenantBucket(tenantID string) string {
	if tenantID == "" {
		return "unknown"
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(tenantID))
	return fmt.Sprintf("%02d", h.Sum32()%TenantBucketModulus)
}

// TenantBucket exposes tenantBucket to other packages (notably the
// observability observers wired in metrics.go) so the same bucketing rule is
// applied at every metric call site. The contract matches tenantBucket
// exactly.
func TenantBucket(tenantID string) string { return tenantBucket(tenantID) }
