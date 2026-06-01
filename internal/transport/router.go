package transport

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/costa92/llm-agent-memory-gateway/internal/authz"
	"github.com/costa92/llm-agent-memory-gateway/internal/httpapi"
	"github.com/costa92/llm-agent-memory-gateway/internal/service"
)

type API struct {
	service service.Interface
}

func NewHandler(svc service.Interface, extras ...func(*http.ServeMux)) http.Handler {
	api := &API{service: svc}

	mux := http.NewServeMux()
	mux.Handle("POST /memory/recall/unified", http.HandlerFunc(api.handleRecallUnified))
	mux.Handle("POST /memory/write", http.HandlerFunc(api.handleWriteMemory))
	mux.Handle("PATCH /memory/items/{memory_id}", http.HandlerFunc(api.handlePatchMemory))
	mux.Handle("POST /memory/items/{memory_id}/pin", http.HandlerFunc(api.handlePinMemory))
	mux.Handle("POST /memory/items/{memory_id}/unpin", http.HandlerFunc(api.handleUnpinMemory))
	mux.Handle("POST /memory/items/{memory_id}/disable", http.HandlerFunc(api.handleDisableMemory))
	mux.Handle("POST /memory/items/{memory_id}/enable", http.HandlerFunc(api.handleEnableMemory))
	mux.Handle("DELETE /memory/items/{memory_id}", http.HandlerFunc(api.handleDeleteMemory))
	mux.Handle("GET /memory/items/{memory_id}", http.HandlerFunc(api.handleGetMemoryItem))
	mux.Handle("POST /memory/sessions/{session_id}/close", http.HandlerFunc(api.handleCloseSession))
	mux.Handle("POST /memory/sessions/{session_id}/heartbeat", http.HandlerFunc(api.handleHeartbeatSession))
	for _, extra := range extras {
		if extra != nil {
			extra(mux)
		}
	}

	return withRequestID(mux)
}

func readAuthoritativeScope(r *http.Request) (authz.Scope, error) {
	scope, err := authz.ScopeFromHeaders(r.Header)
	if err == nil {
		return scope, nil
	}
	if errors.Is(err, authz.ErrUnauthorized) {
		return authz.Scope{}, httpapi.ErrUnauthorized("missing or invalid auth scope headers", nil)
	}
	return authz.Scope{}, err
}

func decodeJSON(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return httpapi.ErrBadRequest("invalid JSON payload", map[string]any{"cause": err.Error()})
	}
	if decoder.More() {
		return httpapi.ErrBadRequest("invalid JSON payload", map[string]any{"cause": "multiple JSON values are not allowed"})
	}
	return nil
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}
