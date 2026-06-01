package transport

import (
	"net/http"

	"github.com/costa92/llm-agent-memory-gateway/internal/httpapi"
)

func (api *API) handleRecallUnified(w http.ResponseWriter, r *http.Request) {
	if !EnsureJSONRequest(w, r) {
		return
	}

	authScope, err := readAuthoritativeScope(r)
	if err != nil {
		httpapi.WriteError(w, RequestIDFromContext(r.Context()), err)
		return
	}

	var req httpapi.RecallUnifiedRequest
	if err := decodeJSON(r, &req); err != nil {
		httpapi.WriteError(w, RequestIDFromContext(r.Context()), err)
		return
	}

	resp, err := api.service.RecallUnified(r.Context(), authScope, req)
	if err != nil {
		httpapi.WriteError(w, RequestIDFromContext(r.Context()), err)
		return
	}

	level := req.ConsistencyLevel
	if resp.Trace != nil && resp.Trace.ConsistencyLevel != "" {
		level = resp.Trace.ConsistencyLevel
	}
	if level == "" {
		level = "eventual"
	}
	SetConsistencyLevelHeader(w, level)
	writeJSON(w, http.StatusOK, resp)
}
