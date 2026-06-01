package service

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"testing"
)

func TestTenantBucket_Stability(t *testing.T) {
	const id = "tenant-stable-42"
	first := tenantBucket(id)
	for i := 0; i < 100; i++ {
		if got := tenantBucket(id); got != first {
			t.Fatalf("non-deterministic: iteration %d got %q, want %q", i, got, first)
		}
	}
}

func TestTenantBucket_Modulus(t *testing.T) {
	// 256 different tenant IDs; each bucket must be in [00..(modulus-1)] formatted as 2 chars.
	for i := 0; i < 256; i++ {
		id := "tenant-" + strconv.Itoa(i)
		got := tenantBucket(id)
		if len(got) != 2 {
			t.Fatalf("bucket %q for id=%q is not 2 chars", got, id)
		}
		n, err := strconv.Atoi(got)
		if err != nil {
			t.Fatalf("bucket %q for id=%q not a number: %v", got, id, err)
		}
		if n < 0 || n >= TenantBucketModulus {
			t.Fatalf("bucket %d for id=%q outside [0,%d)", n, id, TenantBucketModulus)
		}
	}
}

func TestTenantBucket_EmptyTenantID(t *testing.T) {
	if got := tenantBucket(""); got != "unknown" {
		t.Fatalf("empty tenant id → %q, want %q", got, "unknown")
	}
}

func TestTenantBucket_DistributionSanity(t *testing.T) {
	// 1000 random tenant IDs should hit at least 20 distinct buckets out of 32.
	seen := map[string]struct{}{}
	for i := 0; i < 1000; i++ {
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			t.Fatalf("rand.Read: %v", err)
		}
		id := hex.EncodeToString(raw[:])
		bucket := tenantBucket(id)
		if bucket == "unknown" {
			t.Fatalf("random id %q produced 'unknown' bucket", id)
		}
		seen[bucket] = struct{}{}
	}
	if len(seen) < 20 {
		t.Fatalf("only %d distinct buckets across 1000 random IDs, want >= 20", len(seen))
	}
}

func TestTenantBucketModulus_Constant(t *testing.T) {
	if TenantBucketModulus != 32 {
		t.Fatalf("TenantBucketModulus = %d, want 32", TenantBucketModulus)
	}
}
