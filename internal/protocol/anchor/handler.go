// Package anchor implements the bilateral verification API for the L2
// Merkle anchor store written by Sub 3. Patent Application 63/991,596,
// Node 6 read surface — given an event_hash, returns the anchor record
// and Merkle proof so a third party can independently verify on-chain
// inclusion.
//
// Endpoint: GET /v1/protocol/anchor/{event_hash}
//
// Reads only — never mutates the tables.
package anchor

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

// AnchorRecord is the JSON shape returned by the bilateral verification
// endpoint. It combines the anchor metadata with the per-event Merkle
// proof path so the caller has everything needed to verify on-chain
// inclusion without a second round-trip.
type AnchorRecord struct {
	EventHash      string          `json:"event_hash"`
	AnchorID       uuid.UUID       `json:"anchor_id"`
	MerkleRoot     string          `json:"merkle_root"`
	InscriptionID  string          `json:"inscription_id,omitempty"`
	BtcTxID        string          `json:"btc_tx_id,omitempty"`
	BtcBlockHeight int64           `json:"btc_block_height,omitempty"`
	Network        string          `json:"network"`
	AnchorStatus   string          `json:"anchor_status"`
	LeafIndex      int             `json:"leaf_index"`
	MerkleProof    json.RawMessage `json:"merkle_proof"`
	AnchoredAt     time.Time       `json:"anchored_at"`
}

// Handler exposes the anchor read API over a pgx pool.
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
	r.Get("/v1/protocol/anchor/{event_hash}", h.ServeHTTP)
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
			"code":       "not_anchored",
			"event_hash": eventHash,
		})
		return
	case err != nil:
		h.Logger.Error("anchor lookup", zap.String("event_hash", eventHash),
			zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "lookup_failed",
		})
		return
	}

	writeJSON(w, http.StatusOK, rec)
}

// lookup joins protocol.evidence_anchors with protocol.anchors to
// return the full anchor record for the given event_hash.
func lookup(ctx context.Context, pool *pgxpool.Pool, eventHash string) (AnchorRecord, error) {
	const q = `
		SELECT
			ea.event_hash,
			a.anchor_id,
			a.merkle_root,
			COALESCE(a.inscription_id, ''),
			COALESCE(a.btc_tx_id, ''),
			COALESCE(a.btc_block_height, 0),
			a.network,
			a.anchor_status,
			ea.leaf_index,
			ea.merkle_proof,
			a.anchored_at
		FROM protocol.evidence_anchors ea
		JOIN protocol.anchors a USING (anchor_id)
		WHERE ea.event_hash = $1
		ORDER BY a.anchored_at DESC
		LIMIT 1
	`
	var (
		rec      AnchorRecord
		rawProof []byte
	)
	row := pool.QueryRow(ctx, q, eventHash)
	if err := row.Scan(
		&rec.EventHash,
		&rec.AnchorID,
		&rec.MerkleRoot,
		&rec.InscriptionID,
		&rec.BtcTxID,
		&rec.BtcBlockHeight,
		&rec.Network,
		&rec.AnchorStatus,
		&rec.LeafIndex,
		&rawProof,
		&rec.AnchoredAt,
	); err != nil {
		return AnchorRecord{}, err
	}
	rec.MerkleProof = json.RawMessage(rawProof)
	return rec, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
