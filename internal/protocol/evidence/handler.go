// Package evidence implements the bilateral verification API for the
// L1 Evidence Store written by Sub 1. Patent Application 63/991,596,
// Node 3 — the read surface a sender or third party can use to confirm
// that a given event_hash was sealed and to retrieve its chain
// metadata for proof building.
//
// Endpoint: GET /v1/protocol/evidence/{event_hash}
//
// Reads only — never mutates the table.
//
// T-C (sec review C3 + C4): the default response is the
// stripped Record. raw_payload and merchant_id are PII / data-flow
// metadata and are NOT in the public response — a third-party
// verifier brings the payload they're checking, the server just
// confirms the hash sits in the chain. Callers presenting a valid
// API key whose tenant matches the row's merchant_id receive the
// full record (raw_payload + merchant_id) so the merchant can
// reconstruct their own audit chain.
package evidence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/identity"
)

// Record is the public, third-party-safe response shape. No
// merchant_id (data-flow leak), no raw_payload (PII / event content
// leak). All a verifier needs is the chain triplet: event_hash,
// chain_hash, prev_chain_hash. The verifier brings the payload they
// want to check; the server just confirms the hash position.
type Record struct {
	EventID       uuid.UUID `json:"event_id"`
	EventHash     string    `json:"event_hash"`
	ChainHash     string    `json:"chain_hash"`
	PrevChainHash string    `json:"prev_chain_hash,omitempty"`
	SourceCode    string    `json:"source_code"`
	IngestedAt    time.Time `json:"ingested_at"`
}

// RecordFull is returned only when the caller's API key has a
// tenant that matches the row's merchant_id. Adds merchant_id +
// raw_payload so the owning merchant can reconstruct evidence for
// their own audits without re-fetching from upstream.
type RecordFull struct {
	Record
	MerchantID uuid.UUID       `json:"merchant_id"`
	RawPayload json.RawMessage `json:"raw_payload"`
}

// internalRecord is the full row as we read it from the DB. We strip
// to Record before serving the public response, or pass through to
// RecordFull when the caller owns it.
type internalRecord struct {
	EventID       uuid.UUID
	EventHash     string
	ChainHash     string
	PrevChainHash string
	SourceCode    string
	MerchantID    uuid.UUID
	IngestedAt    time.Time
	RawPayload    json.RawMessage
}

// Handler exposes the read API over a pgx pool.
type Handler struct {
	Pool   *pgxpool.Pool
	Logger *zap.Logger
}

// New wires a Handler. Logger may be nil.
func New(pool *pgxpool.Pool, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{Pool: pool, Logger: logger}
}

// Mount registers the route on a chi router.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/v1/protocol/evidence/{event_hash}", h.ServeHTTP)
}

// ServeHTTP implements http.Handler.
//
// The response shape depends on caller identity:
//
//   - Unauthenticated or platform-scope key → public Record (no
//     merchant_id, no raw_payload).
//   - API-key whose claims.TenantID matches the row's merchant_id
//     → RecordFull (adds merchant_id + raw_payload).
//   - API-key whose tenant DOES NOT match → public Record (the row
//     belongs to someone else; we don't leak whose).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	eventHash := chi.URLParam(r, "event_hash")
	if eventHash == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "missing_event_hash",
		})
		return
	}

	full, err := lookup(r.Context(), h.Pool, eventHash)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeJSON(w, http.StatusNotFound, map[string]string{
			"code":       "not_found",
			"event_hash": eventHash,
		})
		return
	case err != nil:
		h.Logger.Error("evidence lookup", zap.String("event_hash", eventHash),
			zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "lookup_failed",
		})
		return
	}

	public := Record{
		EventID:       full.EventID,
		EventHash:     full.EventHash,
		ChainHash:     full.ChainHash,
		PrevChainHash: full.PrevChainHash,
		SourceCode:    full.SourceCode,
		IngestedAt:    full.IngestedAt,
	}

	// Owning-tenant gate: claims must be present, tenant-scoped, and
	// match the row's merchant_id. Anything else → strip to public.
	claims, ok := identity.ClaimsFromContext(r.Context())
	if ok && claims.TenantID != uuid.Nil && claims.TenantID == full.MerchantID {
		writeJSON(w, http.StatusOK, RecordFull{
			Record:     public,
			MerchantID: full.MerchantID,
			RawPayload: full.RawPayload,
		})
		return
	}

	writeJSON(w, http.StatusOK, public)
}

// lookup is exported via the handler only — kept package-private so the
// only public path is HTTP. Returns the full DB row; ServeHTTP decides
// what to send to the wire.
func lookup(ctx context.Context, pool *pgxpool.Pool, eventHash string) (internalRecord, error) {
	const q = `
		SELECT event_id, event_hash, chain_hash,
		       COALESCE(prev_chain_hash, ''),
		       source_code, merchant_id, ingested_at, raw_payload
		FROM protocol.evidence
		WHERE event_hash = $1
	`
	var (
		rec internalRecord
		raw []byte
	)
	row := pool.QueryRow(ctx, q, eventHash)
	if err := row.Scan(
		&rec.EventID, &rec.EventHash, &rec.ChainHash, &rec.PrevChainHash,
		&rec.SourceCode, &rec.MerchantID, &rec.IngestedAt, &raw,
	); err != nil {
		return internalRecord{}, err
	}
	rec.RawPayload = json.RawMessage(raw)
	return rec, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
