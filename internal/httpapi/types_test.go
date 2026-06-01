package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRecallUnifiedJSON(t *testing.T) {
	payload := RecallUnifiedRequest{
		Scope: ScopePayload{
			TenantID:  "tenant-a",
			UserID:    "user-1",
			ProjectID: "proj-x",
			SessionID: "sess-9",
		},
		Query:             "export document as pdf",
		TopK:              8,
		TokenBudget:       1200,
		MemoryTokenBudget: 400,
		ConsistencyLevel:  "eventual",
		AllowStaleCache:   true,
		Debug:             true,
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	body := string(raw)
	for _, want := range []string{
		`"scope":`,
		`"tenant_id":"tenant-a"`,
		`"consistency_level":"eventual"`,
		`"memory_token_budget":400`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("marshal output missing %s: %s", want, body)
		}
	}
}

func TestWriteMemoryJSON(t *testing.T) {
	payload := WriteMemoryRequest{
		IdempotencyKey: "idem_abc_123",
		Scope: ScopePayload{
			TenantID: "tenant-a",
			UserID:   "user-1",
		},
		Record: WriteRecordPayload{
			Kind:       "semantic",
			Source:     "user_saved",
			Category:   "project",
			Content:    "User wants API contract drafts before DB schema.",
			Tags:       []string{"workflow", "preference"},
			Importance: 0.95,
			Pinned:     true,
		},
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	body := string(raw)
	for _, want := range []string{
		`"idempotency_key":"idem_abc_123"`,
		`"kind":"semantic"`,
		`"source":"user_saved"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("marshal output missing %s: %s", want, body)
		}
	}
}

func TestManageAndDeleteJSON(t *testing.T) {
	content := "User wants API contract drafts before table schema."
	importance := 0.97
	tags := []string{"workflow"}

	patch := PatchMemoryRequest{
		Scope: ScopePayload{
			TenantID: "tenant-a",
			UserID:   "user-1",
		},
		ExpectedVersion: 9,
		Patch: PatchMemoryFields{
			Content:    &content,
			Importance: &importance,
			Tags:       &tags,
		},
	}
	deleteReq := DeleteMemoryRequest{
		Scope: ScopePayload{
			TenantID: "tenant-a",
			UserID:   "user-1",
		},
		ExpectedVersion:  9,
		ConsistencyLevel: "strong",
	}
	sessionClose := SessionCloseRequest{
		Scope: ScopePayload{
			TenantID:  "tenant-a",
			UserID:    "user-1",
			SessionID: "sess-9",
		},
		Mode: "expire_working",
	}
	sessionHeartbeat := SessionHeartbeatRequest{
		Scope: ScopePayload{
			TenantID:  "tenant-a",
			UserID:    "user-1",
			SessionID: "sess-9",
		},
	}
	recallResponse := RecallUnifiedResponse{
		Hits: []RecallHitResponse{
			{
				MemoryID: "mem_123",
				Kind:     "semantic",
				Score:    0.95,
				Version:  7,
				Content:  "User prefers concise technical answers.",
				Metadata: RecallHitMetadata{
					MatchedBy:         "long_term_unified",
					TokenCostEstimate: 42,
				},
			},
		},
		Trace: &RecallTraceResponse{
			ConsistencyLevel:      "eventual",
			ReturnedTokenEstimate: 42,
		},
	}

	for name, payload := range map[string]any{
		"patch":             patch,
		"delete":            deleteReq,
		"session_close":     sessionClose,
		"session_heartbeat": sessionHeartbeat,
		"recall_result":     recallResponse,
		"pin":               PinMemoryRequest{ExpectedVersion: 7, Scope: ScopePayload{TenantID: "tenant-a", UserID: "user-1"}},
		"unpin":             PinMemoryRequest{ExpectedVersion: 8, Scope: ScopePayload{TenantID: "tenant-a", UserID: "user-1"}},
		"disable":           DisableMemoryRequest{ExpectedVersion: 7, Scope: ScopePayload{TenantID: "tenant-a", UserID: "user-1"}},
		"enable":            DisableMemoryRequest{ExpectedVersion: 8, Scope: ScopePayload{TenantID: "tenant-a", UserID: "user-1"}},
	} {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("%s json.Marshal() error = %v", name, err)
		}
		body := string(raw)
		switch name {
		case "patch":
			if !strings.Contains(body, `"expected_version":9`) {
				t.Fatalf("patch output missing expected_version: %s", body)
			}
		case "delete":
			if !strings.Contains(body, `"consistency_level":"strong"`) {
				t.Fatalf("delete output missing consistency_level: %s", body)
			}
		case "session_close":
			if !strings.Contains(body, `"mode":"expire_working"`) {
				t.Fatalf("session close output missing mode: %s", body)
			}
		case "session_heartbeat":
			if !strings.Contains(body, `"session_id":"sess-9"`) {
				t.Fatalf("session heartbeat output missing session_id: %s", body)
			}
		case "recall_result":
			if !strings.Contains(body, `"token_cost_estimate":42`) {
				t.Fatalf("recall response output missing token_cost_estimate: %s", body)
			}
		}
	}
}
