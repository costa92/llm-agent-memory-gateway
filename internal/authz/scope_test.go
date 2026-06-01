package authz

import (
	"errors"
	"net/http"
	"testing"
)

func TestScopeFromHeaders_RequiresTenantID(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-User-Id", "user-1")

	_, err := ScopeFromHeaders(headers)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("ScopeFromHeaders() error = %v, want ErrUnauthorized", err)
	}
}

func TestScopeFromHeaders_RequiresUserID(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Tenant-Id", "tenant-1")

	_, err := ScopeFromHeaders(headers)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("ScopeFromHeaders() error = %v, want ErrUnauthorized", err)
	}
}

func TestMergeAuthoritativeScope_OverridesClaimedTenantAndUser(t *testing.T) {
	auth := Scope{
		TenantID:  "tenant-auth",
		UserID:    "user-auth",
		ProjectID: "project-auth",
	}
	claimed := Scope{
		TenantID:  "tenant-claimed",
		UserID:    "user-claimed",
		ProjectID: "project-claimed",
		SessionID: "session-claimed",
	}

	got := MergeAuthoritativeScope(auth, claimed)
	if got.TenantID != "tenant-auth" {
		t.Fatalf("TenantID = %q, want %q", got.TenantID, "tenant-auth")
	}
	if got.UserID != "user-auth" {
		t.Fatalf("UserID = %q, want %q", got.UserID, "user-auth")
	}
	if got.ProjectID != "project-auth" {
		t.Fatalf("ProjectID = %q, want %q", got.ProjectID, "project-auth")
	}
	if got.SessionID != "session-claimed" {
		t.Fatalf("SessionID = %q, want %q", got.SessionID, "session-claimed")
	}
}

func TestMergeAuthoritativeScope_FillsOptionalFieldsFromHeaders(t *testing.T) {
	auth := Scope{
		TenantID:  "tenant-auth",
		UserID:    "user-auth",
		ProjectID: "project-auth",
		SessionID: "session-auth",
	}
	claimed := Scope{
		TenantID: "tenant-claimed",
		UserID:   "user-claimed",
	}

	got := MergeAuthoritativeScope(auth, claimed)
	if got.ProjectID != "project-auth" {
		t.Fatalf("ProjectID = %q, want %q", got.ProjectID, "project-auth")
	}
	if got.SessionID != "session-auth" {
		t.Fatalf("SessionID = %q, want %q", got.SessionID, "session-auth")
	}
}
