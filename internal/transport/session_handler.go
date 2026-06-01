package transport

import (
	"net/http"

	"github.com/costa92/llm-agent-memory-gateway/internal/httpapi"
)

func (api *API) handleCloseSession(w http.ResponseWriter, r *http.Request) {
	if !EnsureJSONRequest(w, r) {
		return
	}

	authScope, err := readAuthoritativeScope(r)
	if err != nil {
		httpapi.WriteError(w, RequestIDFromContext(r.Context()), err)
		return
	}

	var req httpapi.SessionCloseRequest
	if err := decodeJSON(r, &req); err != nil {
		httpapi.WriteError(w, RequestIDFromContext(r.Context()), err)
		return
	}

	resp, err := api.service.CloseSession(r.Context(), authScope, r.PathValue("session_id"), req)
	if err != nil {
		httpapi.WriteError(w, RequestIDFromContext(r.Context()), err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (api *API) handleHeartbeatSession(w http.ResponseWriter, r *http.Request) {
	if !EnsureJSONRequest(w, r) {
		return
	}

	authScope, err := readAuthoritativeScope(r)
	if err != nil {
		httpapi.WriteError(w, RequestIDFromContext(r.Context()), err)
		return
	}

	var req httpapi.SessionHeartbeatRequest
	if err := decodeJSON(r, &req); err != nil {
		httpapi.WriteError(w, RequestIDFromContext(r.Context()), err)
		return
	}

	resp, err := api.service.HeartbeatSession(r.Context(), authScope, r.PathValue("session_id"), req)
	if err != nil {
		httpapi.WriteError(w, RequestIDFromContext(r.Context()), err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
