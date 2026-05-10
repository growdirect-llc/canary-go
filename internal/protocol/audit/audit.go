// Package audit provides chi-compatible HTTP middleware that records
// every state-mutating protocol invocation into app.audit_log.
//
// Designed for the API Gateway (Node 2 of patent Application 63/991,596,
// GRO-746) but written generically so any future protocol service can
// reuse it. One implementation, applied uniformly — no per-handler
// hand-rolling.
//
// Behavior:
//
//   - Captures actor (merchant_id from X-Canary-Merchant), action
//     (METHOD path), source code, payload SHA-256, request_id (X-Request-ID
//     or generated), user_agent, source_ip (RemoteAddr; chi RealIP middleware
//     handles X-Forwarded-For upstream), status_code, latency.
//   - Bridges handler-minted event_id from request context (key
//     CtxKeyEventID) when the webhook handler exposes it.
//   - Inserts into app.audit_log via the supplied Inserter (a thin pgxpool
//     wrapper for production; mockable for tests).
//   - Non-blocking on insert failure: logs a zap warning and continues.
//     Audit gaps are recoverable; refusing webhooks is not.
//
// Patent: every payload — internal or external — traverses the same DMZ
// landing zone, leaving an evidentiary record. Canary is a customer of
// its own protocol (memory: project_canary_is_customer_of_protocol).
//
package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// HeaderMerchant mirrors the gateway's tenant header. Defined here so the
// middleware doesn't import the webhook package (avoids cycles).
const HeaderMerchant = "X-Canary-Merchant"

// HeaderRequestID is the conventional request-id header. If absent on the
// inbound request, the middleware mints a UUID and stamps it back on the
// response so callers can correlate across services.
const HeaderRequestID = "X-Request-ID"

// MaxBodyCapture caps how much of the request body the middleware will
// hash for the digest. Mirrors webhook.MaxBodyBytes intentionally so a
// matching event_hash can be recomputed if needed. Bodies above this are
// already rejected by the handler with 413, so this is a defensive cap.
const MaxBodyCapture = 1 << 20 // 1 MiB

// ctxKey is unexported to keep the context keyspace clean.
type ctxKey int

const (
	// CtxKeyEventID lets the webhook handler push the freshly-minted
	// event_id back onto the request context so the middleware can
	// record it. Handlers that don't mint an event_id (health, etc.)
	// simply don't set this key.
	CtxKeyEventID ctxKey = iota

	// CtxKeySource lets handlers expose the resolved source_code
	// (e.g. "square") even after URL params have been consumed.
	CtxKeySource
)

// WithEventID attaches the gateway's minted event_id to the request
// context so the middleware can record it on the audit row.
func WithEventID(ctx context.Context, eventID uuid.UUID) context.Context {
	return context.WithValue(ctx, CtxKeyEventID, eventID)
}

// WithSource attaches the resolved source_code to the request context.
func WithSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, CtxKeySource, source)
}

// Entry is the materialized audit row. Exported so tests and forensic
// tooling can construct/inspect it without touching SQL.
type Entry struct {
	MerchantID    *uuid.UUID
	Action        string // METHOD path, e.g. "POST /v1/protocol/webhook/square"
	Resource      string // entity_type, e.g. "protocol.event"
	ResourceID    *uuid.UUID
	IPAddress     string
	EventID       *uuid.UUID
	PayloadDigest string // hex-encoded sha256 of request body
	SourceCode    string
	RequestID     string
	UserAgent     string
	StatusCode    int
	LatencyMS     int
	ActorType     string // "agent" | "human" | "system"
	MCPServer     string // service name, e.g. "canary-gateway"
	ToolName      string // for MCP-style invocation; mirrors handler/route name for HTTP
}

// Inserter is the storage seam. Production wires PgxInserter; unit
// tests can supply a mock. Insert error is non-fatal at the response
// path — Middleware logs and (when Spool is configured) pushes the
// entry to the durable backstop.
type Inserter interface {
	Insert(ctx context.Context, e Entry) error
}

// Spooler is the durable backstop for regulated mutations (GRO-956).
// When Inserter.Insert fails AND Config.Regulated marks the request
// as a regulated mutation, Middleware calls Spool.Push to preserve
// the entry for offline retry. Production wires a Valkey-list-backed
// implementation; tests pass a stub.
//
// Push MUST be cheap and reliable — it sits on the request hot path
// when the primary insert failed. Spool errors are logged at Error
// level (not Warn) because they represent the loss of a durable
// audit guarantee for a regulated route.
type Spooler interface {
	Push(ctx context.Context, e Entry) error
}

// Config bundles the dependencies the middleware needs. ServiceName is
// recorded on every row as mcp_server so log queries can scope by
// originating service. ActorType defaults to "agent" if unset (the
// usual case for the gateway — webhooks are agent-driven).
type Config struct {
	Inserter    Inserter
	Logger      *zap.Logger
	ServiceName string
	ActorType   string
	Resource    string // default entity_type; "protocol.event" for the gateway

	// Spool is the optional durable backstop for regulated routes
	// (GRO-956). When non-nil, Insert failures whose request matches
	// Regulated push the Entry to the spool for offline retry. Without
	// a Spool, the middleware preserves the legacy fail-open + Warn-log
	// behavior, but Insert failures on regulated routes log at Error
	// level so dashboards can pivot on durable-audit gaps.
	Spool Spooler

	// Regulated classifies a request as a regulated mutation requiring
	// durable audit. When nil, no requests are regulated — preserves
	// pre-GRO-956 behavior. Production wires a func that matches
	// webhook ingest paths, admin DLQ replay, identity-key lifecycle
	// routes, and any future regulated-write surface.
	Regulated func(*http.Request) bool
}

// Middleware returns a chi-compatible HTTP middleware. It is intentionally
// non-blocking on insert failure — a webhook should not fail because the
// audit log is briefly unreachable.
func Middleware(cfg Config) func(http.Handler) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "canary-gateway"
	}
	if cfg.ActorType == "" {
		cfg.ActorType = "agent"
	}
	if cfg.Resource == "" {
		cfg.Resource = "protocol.event"
	}

	logger := cfg.Logger.With(zap.String("op", "audit.middleware"))

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Stamp request_id (mint if missing). Reflect to response so
			// downstream services can correlate.
			reqID := r.Header.Get(HeaderRequestID)
			if reqID == "" {
				reqID = uuid.NewString()
				r.Header.Set(HeaderRequestID, reqID)
			}
			w.Header().Set(HeaderRequestID, reqID)

			// Capture body for hashing without consuming it for the handler.
			// We cap at MaxBodyCapture; the handler will reject anything
			// beyond MaxBodyBytes anyway via http.MaxBytesReader.
			var bodyBytes []byte
			if r.Body != nil && r.ContentLength != 0 {
				bodyBytes, _ = io.ReadAll(io.LimitReader(r.Body, MaxBodyCapture))
				_ = r.Body.Close()
				r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			}

			// Wrap the response writer so we can read back the status.
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			// Compute digest *after* the handler has run — at this point
			// the bytes we captured have already been validated upstream.
			digest := ""
			if len(bodyBytes) > 0 {
				h := sha256.Sum256(bodyBytes)
				digest = hex.EncodeToString(h[:])
			}

			entry := Entry{
				Action:        r.Method + " " + r.URL.Path,
				Resource:      cfg.Resource,
				IPAddress:     clientIP(r),
				PayloadDigest: digest,
				RequestID:     reqID,
				UserAgent:     r.UserAgent(),
				StatusCode:    ww.Status(),
				LatencyMS:     int(time.Since(start) / time.Millisecond),
				ActorType:     cfg.ActorType,
				MCPServer:     cfg.ServiceName,
				ToolName:      r.URL.Path,
			}

			if mh := r.Header.Get(HeaderMerchant); mh != "" {
				if mid, err := uuid.Parse(mh); err == nil {
					entry.MerchantID = &mid
				}
			}

			ctx := r.Context()
			if v, ok := ctx.Value(CtxKeyEventID).(uuid.UUID); ok {
				ev := v
				entry.EventID = &ev
				entry.ResourceID = &ev
			}
			if v, ok := ctx.Value(CtxKeySource).(string); ok {
				entry.SourceCode = v
			}

			// Insert with a tight timeout so a slow audit DB can't pile
			// up goroutines. The HTTP response has already gone out.
			insertCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err := cfg.Inserter.Insert(insertCtx, entry)
			if err == nil {
				return
			}
			// Insert failed. GRO-956: if this request was a regulated
			// mutation and a Spool is configured, push the entry to
			// the durable backstop. Without a Spool, fall back to the
			// legacy Warn log — but elevate to Error for regulated
			// routes so a missing-Spool deployment is observable.
			regulated := cfg.Regulated != nil && cfg.Regulated(r)
			if regulated && cfg.Spool != nil {
				spoolCtx, spoolCancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer spoolCancel()
				if spoolErr := cfg.Spool.Push(spoolCtx, entry); spoolErr != nil {
					logger.Error("audit insert AND spool failed (durable audit gap)",
						zap.Error(err),
						zap.NamedError("spool_err", spoolErr),
						zap.String("request_id", entry.RequestID),
						zap.String("action", entry.Action),
						zap.Int("status", entry.StatusCode),
					)
					return
				}
				logger.Warn("audit insert failed; entry spooled for retry",
					zap.Error(err),
					zap.String("request_id", entry.RequestID),
					zap.String("action", entry.Action),
				)
				return
			}
			level := zap.WarnLevel
			if regulated {
				// Regulated route + no Spool configured = durable audit
				// gap on this request. Log at Error so the gap surfaces
				// in alerts even before the Spool wiring lands.
				level = zap.ErrorLevel
			}
			logger.Log(level, "audit insert failed",
				zap.Error(err),
				zap.String("request_id", entry.RequestID),
				zap.String("action", entry.Action),
				zap.Int("status", entry.StatusCode),
				zap.Bool("regulated", regulated),
			)
		})
	}
}

// clientIP picks the best IP available. chi's middleware.RealIP runs
// upstream of us in main.go and rewrites RemoteAddr; we still defensively
// inspect X-Forwarded-For in case middleware order changes.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first entry (left-most is original client).
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	return r.RemoteAddr
}

// PgxInserter is the production Inserter: a pgxpool-backed writer that
// inserts each Entry into app.audit_log. Construct via NewPgxInserter.
type PgxInserter struct {
	pool *pgxpool.Pool
}

// NewPgxInserter wraps a pool so the middleware can write to app.audit_log.
func NewPgxInserter(pool *pgxpool.Pool) *PgxInserter {
	return &PgxInserter{pool: pool}
}

// Insert writes one row. The legacy app.audit_log columns (resource,
// resource_id, action, ip_address, merchant_id) carry the existing
// semantics; the protocol columns added in migration 016 carry the rest.
func (p *PgxInserter) Insert(ctx context.Context, e Entry) error {
	const q = `
        INSERT INTO app.audit_log (
            merchant_id, action, resource, resource_id, ip_address,
            event_id, payload_digest, source_code, request_id,
            user_agent, status_code, latency_ms,
            actor_type, mcp_server, tool_name
        ) VALUES (
            $1, $2, $3, $4, $5,
            $6, $7, $8, $9,
            $10, $11, $12,
            $13, $14, $15
        )
    `
	_, err := p.pool.Exec(ctx, q,
		e.MerchantID, e.Action, e.Resource, e.ResourceID, nullable(e.IPAddress),
		e.EventID, nullable(e.PayloadDigest), nullable(e.SourceCode), nullable(e.RequestID),
		nullable(e.UserAgent), e.StatusCode, e.LatencyMS,
		nullable(e.ActorType), nullable(e.MCPServer), nullable(e.ToolName),
	)
	return err
}

// nullable converts empty strings to NULL so we don't pollute indexes
// with empties that should be missing.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// LogRow is the read-shape for app.audit_log rows surfaced to the
// admin portal. Mirrors the Insert columns plus created_at.
type LogRow struct {
	CreatedAt     time.Time
	MerchantID    *uuid.UUID
	Action        string
	Resource      string
	ResourceID    *uuid.UUID
	IPAddress     *string
	EventID       *uuid.UUID
	PayloadDigest *string
	SourceCode    *string
	RequestID     *string
	UserAgent     *string
	StatusCode    *int
	LatencyMS     *int
	ActorType     *string
	MCPServer     *string
	ToolName      *string
}

// ListFilters narrows a ListByMerchant query.
type ListFilters struct {
	MerchantID *uuid.UUID
	SourceCode string
	Action     string
	Limit      int
}

// ListByMerchant returns recent audit rows ordered by created_at DESC.
// Tenant scoping happens via merchant_id (the column carried in the
// audit_log row). Limit clamped to [1, 500] with default 100.
// Wired W9.
func (p *PgxInserter) ListByMerchant(ctx context.Context, f ListFilters) ([]LogRow, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{}
	q := `SELECT created_at, merchant_id, action, resource, resource_id, ip_address,
	             event_id, payload_digest, source_code, request_id, user_agent,
	             status_code, latency_ms, actor_type, mcp_server, tool_name
	      FROM app.audit_log WHERE 1=1`
	if f.MerchantID != nil {
		args = append(args, *f.MerchantID)
		q += " AND merchant_id = $" + itoa(len(args))
	}
	if f.SourceCode != "" {
		args = append(args, f.SourceCode)
		q += " AND source_code = $" + itoa(len(args))
	}
	if f.Action != "" {
		args = append(args, f.Action)
		q += " AND action = $" + itoa(len(args))
	}
	args = append(args, limit)
	q += " ORDER BY created_at DESC LIMIT $" + itoa(len(args))
	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LogRow, 0, limit)
	for rows.Next() {
		var lr LogRow
		if err := rows.Scan(
			&lr.CreatedAt, &lr.MerchantID, &lr.Action, &lr.Resource, &lr.ResourceID, &lr.IPAddress,
			&lr.EventID, &lr.PayloadDigest, &lr.SourceCode, &lr.RequestID, &lr.UserAgent,
			&lr.StatusCode, &lr.LatencyMS, &lr.ActorType, &lr.MCPServer, &lr.ToolName,
		); err != nil {
			return nil, err
		}
		out = append(out, lr)
	}
	return out, rows.Err()
}

// itoa is a tiny private helper so we don't import strconv just for one site.
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	// audit_log filter chain stays small; no need for a generic impl.
	const digits = "0123456789"
	var buf [4]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = digits[n%10]
		n /= 10
	}
	return string(buf[i:])
}
