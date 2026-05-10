// internal/mcp/handler.go
package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/identity"
)

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	errParse             = -32700
	errInvalidReq        = -32600
	errNotFound          = -32601
	errInvalidParams     = -32602
	errInternal          = -32603
	// errInsufficientScope is in JSON-RPC's implementation-defined range
	// (-32000 to -32099 reserved per the spec for server errors).
	// GRO-935 maps the registry's ErrInsufficientScope to this code so
	// clients can distinguish "your key lacks the scope" from a generic
	// internal failure.
	errInsufficientScope = -32001
)

type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolsListResult struct {
	Tools []ToolDef `json:"tools"`
}

type toolCallResult struct {
	Content []contentItem `json:"content"`
}

type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Handler is the MCP JSON-RPC handler.
type Handler struct {
	reg      *Registry
	recorder AuditRecorder
	logger   *zap.Logger
}

// New constructs a Handler with no audit recorder. Mostly used by
// tests; production wiring should use NewWithAudit.
func New(reg *Registry) *Handler { return &Handler{reg: reg} }

// NewWithAudit constructs a Handler that emits an MCP-specific audit
// row for every tools/call dispatch via the supplied recorder. Logger
// is used for failure-to-record warnings (audit insertion is
// fail-open — an audit miss must not turn into a 500).
func NewWithAudit(reg *Registry, recorder AuditRecorder, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{reg: reg, recorder: recorder, logger: logger}
}

func (h *Handler) Mount(r chi.Router) { r.Post("/mcp", h.ServeHTTP) }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req rpcReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, nil, errParse, "parse error")
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		writeErr(w, req.ID, errInvalidReq, "invalid request")
		return
	}

	switch req.Method {
	case "tools/list":
		writeResult(w, req.ID, toolsListResult{Tools: h.reg.List()})

	case "tools/call":
		var p toolsCallParams
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
			writeErr(w, req.ID, errInvalidReq, "params.name required")
			return
		}
		start := time.Now()
		result, callErr := h.reg.Call(r.Context(), p.Name, p.Arguments)
		latency := int(time.Since(start) / time.Millisecond)

		// Decide the status string for the audit row before responding
		// so the same value lands in audit_log even if the handler is
		// interrupted writing the response.
		status := "ok"
		if callErr != nil {
			code := errInternal
			switch {
			case IsUnknownTool(callErr):
				code = errNotFound
			case IsInsufficientScope(callErr):
				code = errInsufficientScope
			case IsInvalidParams(callErr):
				code = errInvalidParams
			}
			status = strconv.Itoa(code)
			h.recordAudit(r, p.Name, p.Arguments, status, latency)
			writeErr(w, req.ID, code, callErr.Error())
			return
		}
		h.recordAudit(r, p.Name, p.Arguments, status, latency)
		text, _ := json.Marshal(result)
		writeResult(w, req.ID, toolCallResult{
			Content: []contentItem{{Type: "text", Text: string(text)}},
		})

	default:
		writeErr(w, req.ID, errNotFound, "method not found: "+req.Method)
	}
}

func writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(rpcResp{JSONRPC: "2.0", ID: id, Result: result})
}

func writeErr(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK) // JSON-RPC errors are always 200
	_ = json.NewEncoder(w).Encode(rpcResp{
		JSONRPC: "2.0", ID: id, Error: &rpcErr{Code: code, Message: msg},
	})
}

// recordAudit writes the MCP-specific audit row when a recorder is
// configured. No-op when recorder is nil so test handlers built via
// New() (without audit) keep working. Fail-open on Record errors —
// the response has already been computed and an audit miss must not
// surface as a 500. The logger captures the miss for later
// investigation.
func (h *Handler) recordAudit(r *http.Request, toolName string, args json.RawMessage, status string, latencyMS int) {
	if h.recorder == nil {
		return
	}
	claims, _ := identity.ClaimsFromContext(r.Context())
	ev := AuditEvent{
		ToolName:   toolName,
		TenantID:   claims.TenantID,
		KeyID:      claims.KeyID,
		ArgsDigest: digestArgs(args),
		Status:     status,
		LatencyMS:  latencyMS,
		RequestID:  r.Header.Get("X-Request-ID"),
	}
	// Use background context with a tight deadline so a slow audit
	// store cannot pile up goroutines or block the response. Mirrors
	// audit.Middleware's pattern.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := h.recorder.Record(ctx, ev); err != nil {
		h.logger.Warn("mcp: audit record failed",
			zap.String("tool", toolName),
			zap.String("status", status),
			zap.String("request_id", ev.RequestID),
			zap.Error(err))
	}
}
