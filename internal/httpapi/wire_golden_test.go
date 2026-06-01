// WIRE CONTRACT GUARD. These golden JSON shapes are the contract with
// github.com/costa92/llm-agent-memory-client, which mirrors (re-declares) these
// DTOs. A diff here is a breaking wire change: re-bless with
//
//	go test ./internal/httpapi/ -run TestWireGolden -update
//
// AND update the client's mirrored types + its golden tests in lockstep
// (gateway first, then client). Renaming/retagging a field, adding/removing
// `omitempty`, or switching pointer-vs-value all trip this test.
package httpapi

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateGolden = flag.Bool("update", false, "regenerate wire golden files under testdata/wire/")

// canonicalize marshals v, then round-trips through a generic interface so that
// any map[string]any (e.g. ErrorBody.Details) is re-emitted with encoding/json's
// deterministic (lexicographic) key ordering. This keeps comparison stable and
// makes the on-disk golden files diff-friendly.
func canonicalize(t *testing.T, v any) []byte {
	t.Helper()

	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	canonical, err := json.MarshalIndent(generic, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error = %v", err)
	}
	return append(canonical, '\n')
}

// assertGolden compares the canonical JSON of value against testdata/wire/<name>.json.
// With -update it (re)writes the golden instead of asserting.
func assertGolden(t *testing.T, name string, value any) {
	t.Helper()

	got := canonicalize(t, value)
	path := filepath.Join("testdata", "wire", name+".json")

	if *updateGolden {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run `go test ./internal/httpapi/ -run TestWireGolden -update` to create it)", path, err)
	}

	if string(got) != string(want) {
		t.Fatalf("wire JSON mismatch for %q.\n--- got ---\n%s\n--- want ---\n%s\nIf this change is intentional, re-bless with -update AND update the mirrored client types + golden tests.", name, got, want)
	}
}

func fullScope() ScopePayload {
	return ScopePayload{
		TenantID:  "tenant-a",
		UserID:    "user-1",
		ProjectID: "proj-x",
		SessionID: "sess-9",
	}
}

func TestWireGolden(t *testing.T) {
	content := "User wants API contract drafts before DB schema."
	category := "project"
	tags := []string{"workflow", "preference"}
	importance := 0.97

	cases := []struct {
		name  string
		value any
	}{
		{
			name: "recall_unified_request",
			value: RecallUnifiedRequest{
				Scope:             fullScope(),
				Query:             "export document as pdf",
				TopK:              8,
				TokenBudget:       1200,
				MemoryTokenBudget: 400,
				ConsistencyLevel:  "eventual",
				AllowStaleCache:   true,
				Debug:             true,
			},
		},
		{
			name: "recall_unified_response",
			value: RecallUnifiedResponse{
				Hits: []RecallHitResponse{
					{
						MemoryID: "mem_123",
						Kind:     "semantic",
						Score:    0.95,
						Version:  7,
						Content:  "User prefers concise technical answers.",
						Tags:     []string{"preference", "style"},
						Source:   "user_saved",
						Category: "profile",
						Pinned:   true,
						Disabled: false,
						Metadata: RecallHitMetadata{
							MatchedBy:         "long_term_unified",
							TokenCostEstimate: 42,
						},
					},
				},
				Trace: &RecallTraceResponse{
					CacheLevel:            "l1",
					ConsistencyLevel:      "eventual",
					StaleServed:           true,
					MemoryTokenBudget:     400,
					ReturnedTokenEstimate: 42,
				},
			},
		},
		{
			name: "write_memory_request",
			value: WriteMemoryRequest{
				IdempotencyKey: "idem_abc_123",
				Scope:          fullScope(),
				Record: WriteRecordPayload{
					Kind:       "semantic",
					Source:     "user_saved",
					Category:   "project",
					Content:    "User wants API contract drafts before DB schema.",
					Tags:       []string{"workflow", "preference"},
					Importance: 0.95,
					Pinned:     true,
				},
			},
		},
		{
			name: "write_memory_response",
			value: WriteMemoryResponse{
				Memory: WriteMemoryResult{
					MemoryID: "mem_123",
					Version:  7,
					Status:   "created",
				},
			},
		},
		{
			name: "patch_memory_request",
			value: PatchMemoryRequest{
				IdempotencyKey:  "idem_patch_1",
				Scope:           fullScope(),
				ExpectedVersion: 9,
				Patch: PatchMemoryFields{
					Content:    &content,
					Category:   &category,
					Tags:       &tags,
					Importance: &importance,
				},
			},
		},
		{
			// PATCH omitempty guard: nil pointer fields MUST be absent from JSON.
			// This is the exact semantic the client depends on (partial update).
			name: "patch_memory_request_nil_fields",
			value: PatchMemoryRequest{
				Scope:           fullScope(),
				ExpectedVersion: 9,
				Patch:           PatchMemoryFields{},
			},
		},
		{
			name: "patch_memory_response",
			value: PatchMemoryResponse{
				MemoryID: "mem_123",
				Version:  10,
			},
		},
		{
			name: "pin_memory_request",
			value: PinMemoryRequest{
				Scope:           fullScope(),
				ExpectedVersion: 7,
			},
		},
		{
			name: "pin_memory_response",
			value: PinMemoryResponse{
				MemoryID: "mem_123",
				Version:  8,
				Pinned:   true,
			},
		},
		{
			name: "disable_memory_request",
			value: DisableMemoryRequest{
				Scope:           fullScope(),
				ExpectedVersion: 7,
			},
		},
		{
			name: "disable_memory_response",
			value: DisableMemoryResponse{
				MemoryID: "mem_123",
				Version:  8,
				Disabled: true,
			},
		},
		{
			name: "delete_memory_request",
			value: DeleteMemoryRequest{
				Scope:            fullScope(),
				ExpectedVersion:  9,
				ConsistencyLevel: "strong",
			},
		},
		{
			name: "delete_memory_response",
			value: DeleteMemoryResponse{
				MemoryID: "mem_123",
				Deleted:  true,
				Version:  10,
			},
		},
		{
			name: "get_memory_item_response",
			value: GetMemoryItemResponse{
				MemoryID:   "mem_123",
				Kind:       "semantic",
				Version:    7,
				Content:    "User prefers concise technical answers.",
				Tags:       []string{"preference", "style"},
				Source:     "user_saved",
				Category:   "profile",
				Importance: 0.95,
				Pinned:     true,
				Disabled:   false,
			},
		},
		{
			name: "session_close_request",
			value: SessionCloseRequest{
				Scope: fullScope(),
				Mode:  "expire_working",
			},
		},
		{
			name: "session_close_response",
			value: SessionCloseResponse{
				SessionID: "sess-9",
				Status:    "closed",
			},
		},
		{
			name: "session_heartbeat_request",
			value: SessionHeartbeatRequest{
				Scope: fullScope(),
			},
		},
		{
			name: "session_heartbeat_response",
			value: SessionHeartbeatResponse{
				SessionID: "sess-9",
				Status:    "active",
			},
		},
		{
			// Error envelope as emitted by WriteError, with a fully-populated
			// details map. Details is the only map[string]any in the wire
			// surface; canonicalize() pins its key ordering deterministically.
			name: "error_response",
			value: ErrorResponse{
				Error: ErrorBody{
					Code:      "memory_conflict",
					Message:   "expected_version does not match current version",
					RequestID: "req_123",
					Retryable: false,
					Details: map[string]any{
						"memory_id":        "mem_123",
						"expected_version": 4,
						"current_version":  5,
					},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertGolden(t, tc.name, tc.value)
		})
	}
}

// TestWireGoldenPatchNilFieldsOmitted is an explicit, self-contained guard that
// the PATCH pointer fields use omitempty: with all of them nil, the only keys in
// `patch` must be none. This complements the patch_memory_request_nil_fields
// golden by asserting the semantic directly (independent of the golden file).
func TestWireGoldenPatchNilFieldsOmitted(t *testing.T) {
	raw, err := json.Marshal(PatchMemoryFields{})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if string(raw) != "{}" {
		t.Fatalf("nil PatchMemoryFields must marshal to {} (all pointer fields omitempty); got %s", raw)
	}
}
