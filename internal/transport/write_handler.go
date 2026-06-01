package transport

import (
	"net/http"

	"github.com/costa92/llm-agent-memory-gateway/internal/httpapi"
)

func (api *API) handleWriteMemory(w http.ResponseWriter, r *http.Request) {
	if !EnsureJSONRequest(w, r) {
		return
	}

	authScope, err := readAuthoritativeScope(r)
	if err != nil {
		httpapi.WriteError(w, RequestIDFromContext(r.Context()), err)
		return
	}

	var req httpapi.WriteMemoryRequest
	if err := decodeJSON(r, &req); err != nil {
		httpapi.WriteError(w, RequestIDFromContext(r.Context()), err)
		return
	}

	resp, err := api.service.WriteMemory(r.Context(), authScope, req)
	if err != nil {
		httpapi.WriteError(w, RequestIDFromContext(r.Context()), err)
		return
	}

	SetMemoryVersionHeader(w, resp.Memory.Version)
	writeJSON(w, http.StatusOK, resp)
}
