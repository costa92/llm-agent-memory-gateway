package transport

import (
	"context"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/costa92/llm-agent-memory-gateway/internal/httpapi"
)

type contextKey string

const requestIDContextKey contextKey = "request_id"

var requestIDCounter atomic.Uint64

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-Id")
		if requestID == "" {
			requestID = newRequestID()
		}

		ctx := context.WithValue(r.Context(), requestIDContextKey, requestID)
		w.Header().Set("X-Request-Id", requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey).(string)
	return requestID
}

func EnsureJSONRequest(w http.ResponseWriter, r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPatch, http.MethodDelete:
	default:
		return true
	}

	if r.Header.Get("Content-Type") != "application/json" {
		httpapi.WriteError(w, RequestIDFromContext(r.Context()), httpapi.ErrBadRequest("Content-Type must be application/json", nil))
		return false
	}
	return true
}

func SetMemoryVersionHeader(w http.ResponseWriter, version int64) {
	if version <= 0 {
		return
	}
	w.Header().Set("X-Memory-Version", strconv.FormatInt(version, 10))
}

func SetConsistencyLevelHeader(w http.ResponseWriter, level string) {
	if level == "" {
		return
	}
	w.Header().Set("X-Consistency-Level", level)
}

func newRequestID() string {
	seq := requestIDCounter.Add(1)
	return "req_" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10) + "_" + strconv.FormatUint(seq, 10)
}
