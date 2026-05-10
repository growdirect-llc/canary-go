package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/google/uuid"
)

// AuditEvent is the MCP-specific audit shape (GRO-936). Captured per
// tools/call dispatch so MCP mutations leave an evidentiary trail
// matching the webhook + admin surfaces.
//
// Args are NOT recorded raw — they may contain customer PII passed
// through tool arguments. Instead, ArgsDigest is the SHA-256 hex of
// the canonical JSON arguments, sufficient to prove what was sent
// without requiring the storage layer to handle PII redaction.
type AuditEvent struct {
	// ToolName is the canary.<module>.<verb> identifier.
	ToolName string

	// TenantID is the caller's tenant from API-key claims (uuid.Nil
	// for platform-scope keys, but the gateway already gates those).
	TenantID uuid.UUID

	// KeyID is the API-key UUID (claims.KeyID), recorded so a single
	// compromised key's actions can be reconstructed for incident
	// response.
	KeyID uuid.UUID

	// ArgsDigest is the SHA-256 hex of the raw arguments bytes as
	// received. Empty for tool calls with no arguments.
	ArgsDigest string

	// Status is "ok" on success, the JSON-RPC error code as a string
	// on failure ("-32601" for unknown_tool, "-32001" for insufficient_scope,
	// etc.). String to make filtering on "error class" trivial without
	// joining against the JSON-RPC code table.
	Status string

	// LatencyMS is wall-clock milliseconds spent in dispatch — useful
	// for spotting anomalous tool durations during forensics.
	LatencyMS int

	// RequestID mirrors the audit.HeaderRequestID stamped by the
	// chi RequestID middleware so MCP rows correlate with HTTP-level
	// audit_log rows for the same call.
	RequestID string
}

// AuditRecorder is the storage seam (matches the audit.Inserter
// pattern). cmd/gateway wires an adapter that maps AuditEvent into
// the existing audit.Inserter so MCP rows land in the same
// app.audit_log table as the rest of the gateway's state changes.
type AuditRecorder interface {
	Record(ctx context.Context, e AuditEvent) error
}

// digestArgs returns the SHA-256 hex of args. Empty bytes (no
// arguments) returns "". json.RawMessage is hashed as-is — we don't
// canonicalize whitespace, the caller already produced a consistent
// shape because the JSON-RPC layer parsed it once.
func digestArgs(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	h := sha256.Sum256(args)
	return hex.EncodeToString(h[:])
}
