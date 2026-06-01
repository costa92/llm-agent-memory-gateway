package authz

import (
	"errors"
	"net/http"
	"strings"
)

var ErrUnauthorized = errors.New("unauthorized")

type Scope struct {
	TenantID  string
	UserID    string
	ProjectID string
	SessionID string
}

func ScopeFromHeaders(h http.Header) (Scope, error) {
	scope := Scope{
		TenantID:  strings.TrimSpace(h.Get("X-Tenant-Id")),
		UserID:    strings.TrimSpace(h.Get("X-User-Id")),
		ProjectID: strings.TrimSpace(h.Get("X-Project-Id")),
		SessionID: strings.TrimSpace(h.Get("X-Session-Id")),
	}

	if scope.TenantID == "" || scope.UserID == "" {
		return Scope{}, ErrUnauthorized
	}

	return scope, nil
}

func MergeAuthoritativeScope(auth Scope, claimed Scope) Scope {
	merged := claimed
	merged.TenantID = auth.TenantID
	merged.UserID = auth.UserID

	if auth.ProjectID != "" {
		merged.ProjectID = auth.ProjectID
	}
	if auth.SessionID != "" {
		merged.SessionID = auth.SessionID
	}

	return merged
}
