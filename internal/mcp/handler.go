// internal/mcp/handler.go
package mcp

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
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
type Handler struct{ reg *Registry }

func New(reg *Registry) *Handler { return &Handler{reg: reg} }

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
		result, err := h.reg.Call(r.Context(), p.Name, p.Arguments)
		if err != nil {
			code := errInternal
			switch {
			case IsUnknownTool(err):
				code = errNotFound
			case IsInsufficientScope(err):
				code = errInsufficientScope
			}
			writeErr(w, req.ID, code, err.Error())
			return
		}
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
