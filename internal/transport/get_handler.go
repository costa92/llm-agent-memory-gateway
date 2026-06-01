package transport

import (
	"net/http"

	"github.com/costa92/llm-agent-memory-gateway/internal/httpapi"
)

func (api *API) handleGetMemoryItem(w http.ResponseWriter, r *http.Request) {
	authScope, err := readAuthoritativeScope(r)
	if err != nil {
		httpapi.WriteError(w, RequestIDFromContext(r.Context()), err)
		return
	}

	resp, err := api.service.GetMemoryItem(r.Context(), authScope, r.PathValue("memory_id"))
	if err != nil {
		httpapi.WriteError(w, RequestIDFromContext(r.Context()), err)
		return
	}

	SetMemoryVersionHeader(w, resp.Version)
	writeJSON(w, http.StatusOK, resp)
}
