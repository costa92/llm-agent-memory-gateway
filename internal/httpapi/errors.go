package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
)

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"request_id"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

type GatewayError struct {
	code      string
	message   string
	retryable bool
	details   map[string]any
}

func (e *GatewayError) Error() string {
	return e.message
}

func (e *GatewayError) Code() string {
	return e.code
}

func (e *GatewayError) Retryable() bool {
	return e.retryable
}

func (e *GatewayError) Details() map[string]any {
	return e.details
}

func newGatewayError(code, message string, retryable bool, details map[string]any) error {
	return &GatewayError{
		code:      code,
		message:   message,
		retryable: retryable,
		details:   details,
	}
}

func ErrBadRequest(message string, details map[string]any) error {
	return newGatewayError("bad_request", message, false, details)
}

func ErrUnauthorized(message string, details map[string]any) error {
	return newGatewayError("unauthorized", message, false, details)
}

func ErrForbidden(message string, details map[string]any) error {
	return newGatewayError("forbidden", message, false, details)
}

func ErrSessionExpired(message string, details map[string]any) error {
	return newGatewayError("session_expired", message, false, details)
}

func ErrNotFound(message string, details map[string]any) error {
	return newGatewayError("not_found", message, false, details)
}

func ErrMemoryConflict(message string, details map[string]any) error {
	return newGatewayError("memory_conflict", message, false, details)
}

func ErrIdempotencyConflict(message string, details map[string]any) error {
	return newGatewayError("idempotency_conflict", message, false, details)
}

func ErrReadOnlyMode(message string, details map[string]any) error {
	return newGatewayError("read_only_mode", message, true, details)
}

func ErrUpstreamUnavailable(message string, details map[string]any) error {
	return newGatewayError("upstream_unavailable", message, true, details)
}

func StatusCode(err error) int {
	var gatewayErr *GatewayError
	if !errors.As(err, &gatewayErr) {
		return http.StatusInternalServerError
	}

	switch gatewayErr.code {
	case "bad_request":
		return http.StatusBadRequest
	case "unauthorized":
		return http.StatusUnauthorized
	case "forbidden", "session_expired":
		return http.StatusForbidden
	case "not_found":
		return http.StatusNotFound
	case "memory_conflict", "idempotency_conflict":
		return http.StatusConflict
	case "read_only_mode":
		return http.StatusServiceUnavailable
	case "upstream_unavailable":
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func WriteError(w http.ResponseWriter, requestID string, err error) {
	statusCode := StatusCode(err)

	var gatewayErr *GatewayError
	if !errors.As(err, &gatewayErr) {
		gatewayErr = &GatewayError{
			code:      "internal_error",
			message:   http.StatusText(http.StatusInternalServerError),
			retryable: false,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	response := ErrorResponse{
		Error: ErrorBody{
			Code:      gatewayErr.code,
			Message:   gatewayErr.message,
			RequestID: requestID,
			Retryable: gatewayErr.retryable,
			Details:   gatewayErr.details,
		},
	}

	_ = json.NewEncoder(w).Encode(response)
}
