package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddleware_AddsRequestID(t *testing.T) {
	handler := withRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if RequestIDFromContext(r.Context()) == "" {
			t.Fatal("request id missing from context")
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Header().Get("X-Request-Id") == "" {
		t.Fatal("X-Request-Id header missing")
	}
}

func TestMiddleware_PreservesIncomingRequestID(t *testing.T) {
	handler := withRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := RequestIDFromContext(r.Context()); got != "req_existing" {
			t.Fatalf("RequestIDFromContext() = %q, want %q", got, "req_existing")
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "req_existing")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if got := recorder.Header().Get("X-Request-Id"); got != "req_existing" {
		t.Fatalf("X-Request-Id = %q, want %q", got, "req_existing")
	}
}

func TestMiddleware_ResponseHeadersHelpers(t *testing.T) {
	recorder := httptest.NewRecorder()

	SetMemoryVersionHeader(recorder, 7)
	SetConsistencyLevelHeader(recorder, "strong")

	if got := recorder.Header().Get("X-Memory-Version"); got != "7" {
		t.Fatalf("X-Memory-Version = %q, want 7", got)
	}
	if got := recorder.Header().Get("X-Consistency-Level"); got != "strong" {
		t.Fatalf("X-Consistency-Level = %q, want strong", got)
	}
}

func TestRequestIDFromContext_EmptyByDefault(t *testing.T) {
	if got := RequestIDFromContext(context.Background()); got != "" {
		t.Fatalf("RequestIDFromContext() = %q, want empty", got)
	}
}
