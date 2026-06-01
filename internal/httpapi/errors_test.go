package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestErrorStatus(t *testing.T) {
	testCases := []struct {
		name string
		err  error
		want int
	}{
		{name: "bad request", err: ErrBadRequest("bad input", nil), want: http.StatusBadRequest},
		{name: "unauthorized", err: ErrUnauthorized("missing auth", nil), want: http.StatusUnauthorized},
		{name: "forbidden", err: ErrForbidden("forbidden", nil), want: http.StatusForbidden},
		{name: "session expired", err: ErrSessionExpired("expired", nil), want: http.StatusForbidden},
		{name: "not found", err: ErrNotFound("missing", nil), want: http.StatusNotFound},
		{name: "memory conflict", err: ErrMemoryConflict("version mismatch", nil), want: http.StatusConflict},
		{name: "idempotency conflict", err: ErrIdempotencyConflict("payload mismatch", nil), want: http.StatusConflict},
		{name: "read only", err: ErrReadOnlyMode("writes disabled", nil), want: http.StatusServiceUnavailable},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StatusCode(tc.err); got != tc.want {
				t.Fatalf("StatusCode() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestWriteError(t *testing.T) {
	recorder := httptest.NewRecorder()

	WriteError(
		recorder,
		"req_123",
		ErrMemoryConflict(
			"expected_version does not match current version",
			map[string]any{
				"memory_id":        "mem_123",
				"expected_version": 4,
				"current_version":  5,
			},
		),
	)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var response ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if response.Error.Code != "memory_conflict" {
		t.Fatalf("code = %q, want %q", response.Error.Code, "memory_conflict")
	}
	if response.Error.Message != "expected_version does not match current version" {
		t.Fatalf("message = %q", response.Error.Message)
	}
	if response.Error.RequestID != "req_123" {
		t.Fatalf("request_id = %q, want %q", response.Error.RequestID, "req_123")
	}
	if response.Error.Retryable {
		t.Fatal("retryable = true, want false")
	}
	if got := response.Error.Details["memory_id"]; got != "mem_123" {
		t.Fatalf("details[memory_id] = %v, want mem_123", got)
	}
	if got := response.Error.Details["expected_version"]; got != float64(4) {
		t.Fatalf("details[expected_version] = %v, want 4", got)
	}
	if got := response.Error.Details["current_version"]; got != float64(5) {
		t.Fatalf("details[current_version] = %v, want 5", got)
	}
}
