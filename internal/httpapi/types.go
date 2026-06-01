package httpapi

type ScopePayload struct {
	TenantID  string `json:"tenant_id"`
	UserID    string `json:"user_id"`
	ProjectID string `json:"project_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

type RecallUnifiedRequest struct {
	Scope             ScopePayload `json:"scope"`
	Query             string       `json:"query"`
	TopK              int          `json:"top_k,omitempty"`
	TokenBudget       int          `json:"token_budget,omitempty"`
	MemoryTokenBudget int          `json:"memory_token_budget,omitempty"`
	ConsistencyLevel  string       `json:"consistency_level,omitempty"`
	AllowStaleCache   bool         `json:"allow_stale_cache,omitempty"`
	Debug             bool         `json:"debug,omitempty"`
}

type RecallUnifiedResponse struct {
	Hits  []RecallHitResponse  `json:"hits"`
	Trace *RecallTraceResponse `json:"trace,omitempty"`
}

type RecallHitResponse struct {
	MemoryID string            `json:"memory_id"`
	Kind     string            `json:"kind"`
	Score    float64           `json:"score"`
	Version  int64             `json:"version"`
	Content  string            `json:"content"`
	Tags     []string          `json:"tags,omitempty"`
	Source   string            `json:"source"`
	Category string            `json:"category"`
	Pinned   bool              `json:"pinned"`
	Disabled bool              `json:"disabled"`
	Metadata RecallHitMetadata `json:"metadata"`
}

type RecallHitMetadata struct {
	MatchedBy         string `json:"matched_by,omitempty"`
	TokenCostEstimate int    `json:"token_cost_estimate,omitempty"`
}

type RecallTraceResponse struct {
	CacheLevel            string `json:"cache_level,omitempty"`
	ConsistencyLevel      string `json:"consistency_level,omitempty"`
	StaleServed           bool   `json:"stale_served"`
	MemoryTokenBudget     int    `json:"memory_token_budget,omitempty"`
	ReturnedTokenEstimate int    `json:"returned_token_estimate,omitempty"`
}

type WriteMemoryRequest struct {
	IdempotencyKey string             `json:"idempotency_key"`
	Scope          ScopePayload       `json:"scope"`
	Record         WriteRecordPayload `json:"record"`
}

type WriteRecordPayload struct {
	Kind       string   `json:"kind"`
	Source     string   `json:"source"`
	Category   string   `json:"category"`
	Content    string   `json:"content"`
	Tags       []string `json:"tags,omitempty"`
	Importance float64  `json:"importance,omitempty"`
	Pinned     bool     `json:"pinned"`
}

type WriteMemoryResponse struct {
	Memory WriteMemoryResult `json:"memory"`
}

type WriteMemoryResult struct {
	MemoryID string `json:"memory_id"`
	Version  int64  `json:"version"`
	Status   string `json:"status"`
}

type PatchMemoryRequest struct {
	IdempotencyKey  string            `json:"idempotency_key,omitempty"`
	Scope           ScopePayload      `json:"scope"`
	ExpectedVersion int64             `json:"expected_version"`
	Patch           PatchMemoryFields `json:"patch"`
}

type PatchMemoryFields struct {
	Content    *string   `json:"content,omitempty"`
	Category   *string   `json:"category,omitempty"`
	Tags       *[]string `json:"tags,omitempty"`
	Importance *float64  `json:"importance,omitempty"`
}

type PatchMemoryResponse struct {
	MemoryID string `json:"memory_id"`
	Version  int64  `json:"version"`
}

type PinMemoryRequest struct {
	Scope           ScopePayload `json:"scope"`
	ExpectedVersion int64        `json:"expected_version"`
}

type PinMemoryResponse struct {
	MemoryID string `json:"memory_id"`
	Version  int64  `json:"version"`
	Pinned   bool   `json:"pinned"`
}

type DisableMemoryRequest struct {
	Scope           ScopePayload `json:"scope"`
	ExpectedVersion int64        `json:"expected_version"`
}

type DisableMemoryResponse struct {
	MemoryID string `json:"memory_id"`
	Version  int64  `json:"version"`
	Disabled bool   `json:"disabled"`
}

type DeleteMemoryRequest struct {
	Scope            ScopePayload `json:"scope"`
	ExpectedVersion  int64        `json:"expected_version"`
	ConsistencyLevel string       `json:"consistency_level,omitempty"`
}

type DeleteMemoryResponse struct {
	MemoryID string `json:"memory_id"`
	Deleted  bool   `json:"deleted"`
	Version  int64  `json:"version"`
}

type SessionCloseRequest struct {
	Scope ScopePayload `json:"scope"`
	Mode  string       `json:"mode"`
}

type SessionCloseResponse struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
}

type SessionHeartbeatRequest struct {
	Scope ScopePayload `json:"scope"`
}

type SessionHeartbeatResponse struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
}

type GetMemoryItemResponse struct {
	MemoryID   string   `json:"memory_id"`
	Kind       string   `json:"kind"`
	Version    int64    `json:"version"`
	Content    string   `json:"content"`
	Tags       []string `json:"tags,omitempty"`
	Source     string   `json:"source"`
	Category   string   `json:"category"`
	Importance float64  `json:"importance,omitempty"`
	Pinned     bool     `json:"pinned"`
	Disabled   bool     `json:"disabled"`
}
