// Package evidence implements the bilateral verification API for the
// L1 Evidence Store written by Sub 1. Patent Application 63/991,596,
// Node 3 — the read surface a sender or third party can use to confirm
// that a given event_hash was sealed and to retrieve its chain
// metadata for proof building.
//
// Endpoint: GET /v1/protocol/evidence/{event_hash}
//
// Reads only — never mutates the table.
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
)

// Record is the JSON shape returned to bilateral verifiers. raw_payload
// is included so the verifier can independently re-hash it and confirm
// the event_hash they're checking against; without this, "verify the
// hash matches" requires already trusting a copy of the payload.
type Record struct {
	EventID       uuid.UUID       `json:"event_id"`
	EventHash     string          `json:"event_hash"`
	ChainHash     string          `json:"chain_hash"`
	PrevChainHash string          `json:"prev_chain_hash,omitempty"`
	SourceCode    string          `json:"source_code"`
	MerchantID    uuid.UUID       `json:"merchant_id"`
	IngestedAt    time.Time       `json:"ingested_at"`
	RawPayload    json.RawMessage `json:"raw_payload"`
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
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	eventHash := chi.URLParam(r, "event_hash")
	if eventHash == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "missing_event_hash",
		})
		return
	}

	rec, err := lookup(r.Context(), h.Pool, eventHash)
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

	writeJSON(w, http.StatusOK, rec)
}

// lookup is exported via the handler only — kept package-private so the
// only public path is HTTP.
func lookup(ctx context.Context, pool *pgxpool.Pool, eventHash string) (Record, error) {
	const q = `
		SELECT event_id, event_hash, chain_hash,
		       COALESCE(prev_chain_hash, ''),
		       source_code, merchant_id, ingested_at, raw_payload
		FROM protocol.evidence
		WHERE event_hash = $1
	`
	var (
		rec Record
		raw []byte
	)
	row := pool.QueryRow(ctx, q, eventHash)
	if err := row.Scan(
		&rec.EventID, &rec.EventHash, &rec.ChainHash, &rec.PrevChainHash,
		&rec.SourceCode, &rec.MerchantID, &rec.IngestedAt, &raw,
	); err != nil {
		return Record{}, err
	}
	rec.RawPayload = json.RawMessage(raw)
	return rec, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
