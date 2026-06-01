package transport

import (
	"net/http"

	"github.com/costa92/llm-agent-memory-gateway/internal/httpapi"
)

func (api *API) handleUnpinMemory(w http.ResponseWriter, r *http.Request) {
	if !EnsureJSONRequest(w, r) {
		return
	}

	authScope, err := readAuthoritativeScope(r)
	if err != nil {
		httpapi.WriteError(w, RequestIDFromContext(r.Context()), err)
		return
	}

	var req httpapi.PinMemoryRequest
	if err := decodeJSON(r, &req); err != nil {
		httpapi.WriteError(w, RequestIDFromContext(r.Context()), err)
		return
	}

	resp, err := api.service.UnpinMemory(r.Context(), authScope, r.PathValue("memory_id"), req)
	if err != nil {
		httpapi.WriteError(w, RequestIDFromContext(r.Context()), err)
		return
	}

	SetMemoryVersionHeader(w, resp.Version)
	writeJSON(w, http.StatusOK, resp)
}
