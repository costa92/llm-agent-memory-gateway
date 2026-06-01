// Package memorygateway is the HTTP gateway and service-composition layer for
// llm-agent-memory. It owns transport, auth-derived scope binding, runtime
// configuration, and backend wiring over the SDK plus concrete durable
// backends. M7 adds best-effort persisted decision traces (via the
// memory_decision_trace table) and the M7 validation-metric subset on the
// /metrics endpoint.
package memorygateway
