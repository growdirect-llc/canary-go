package validate

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/growdirect-llc/rapidpos/internal/protocol/sub3"
)

// Handler exposes the L402 sat-gated verification API.
//
//	POST /v1/protocol/verify         — issue L402 challenge or consume if auth present
//	GET  /v1/protocol/verify/{token_id} — consume a token and return the Merkle proof
type Handler struct {
	Store        ValidationStore
	L402         *StubL402
	Logger       *zap.Logger
	SatoshiPrice int64
}

// Mount registers both routes on a chi router.
func (h *Handler) Mount(r chi.Router) {
	r.Post("/v1/protocol/verify", h.issueChallenge)
	r.Get("/v1/protocol/verify/{token_id}", h.consumeToken)
}

// VerificationResponse is the JSON body returned on successful proof retrieval.
type VerificationResponse struct {
	EventHash      string          `json:"event_hash"`
	AnchorID       uuid.UUID       `json:"anchor_id"`
	MerkleRoot     string          `json:"merkle_root"`
	InscriptionID  *string         `json:"inscription_id"`
	BtcTxID        *string         `json:"btc_tx_id"`
	BtcBlockHeight *int64          `json:"btc_block_height"`
	Network        string          `json:"network"`
	AnchorStatus   string          `json:"anchor_status"`
	LeafIndex      int             `json:"leaf_index"`
	MerkleProof    []sub3.ProofNode `json:"merkle_proof"`
	AnchoredAt     time.Time       `json:"anchored_at"`
	Verified       bool            `json:"verified"`
	SatoshiPrice   int64           `json:"satoshi_price"`
}

// ─── POST /v1/protocol/verify ────────────────────────────────────────────────

type verifyRequest struct {
	EventHash string `json:"event_hash"`
}

// issueChallenge handles POST /v1/protocol/verify.
//
// Flow:
//  1. Validate event_hash is a 64-char hex string (SHA-256).
//  2. Check Authorization header for a valid L402 macaroon.
//     - If valid: look up token, consume, return proof.
//  3. Otherwise: check anchor status.
//     - Not anchored → 200 verified:false.
//     - Not found → 404.
//     - Anchored → issue 402 with WWW-Authenticate header.
func (h *Handler) issueChallenge(w http.ResponseWriter, r *http.Request) {
	var req verifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	if !isValidEventHash(req.EventHash) {
		writeError(w, http.StatusBadRequest, "invalid_event_hash",
			"event_hash must be a 64-character lowercase hex string (SHA-256)")
		return
	}

	// Check for Authorization: L402 macaroon="<mac>",token_id="<id>"
	if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "L402 ") {
		tokenID, macaroon, ok := parseL402Header(authHeader)
		if ok && h.L402.VerifyMacaroon(tokenID, macaroon) {
			// Auth is valid — consume the token and return the proof.
			h.consumeAndRespond(w, r, tokenID)
			return
		}
	}

	// No valid auth — check anchor status.
	proof, err := h.Store.GetAnchorProof(r.Context(), req.EventHash)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found",
			fmt.Sprintf("event_hash %s not found in evidence store", req.EventHash))
		return
	case errors.Is(err, ErrNotAnchored):
		// Event exists but not yet on-chain — return 200 unverified.
		writeJSON(w, http.StatusOK, map[string]any{
			"verified":      false,
			"anchor_status": "pending",
			"message":       "event not yet anchored",
			"event_hash":    req.EventHash,
		})
		return
	case err != nil:
		h.Logger.Error("issueChallenge get_anchor_proof",
			zap.String("event_hash", req.EventHash), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "lookup_failed", "")
		return
	}

	// Event is anchored — issue L402 challenge.
	tok, err := h.Store.InsertToken(r.Context(), proof.EventHash, h.SatoshiPrice)
	if err != nil {
		h.Logger.Error("issueChallenge insert_token", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "token_create_failed", "")
		return
	}

	mac, invoice := h.L402.IssueChallenge(tok.TokenID, h.SatoshiPrice)
	w.Header().Set("WWW-Authenticate",
		fmt.Sprintf(`L402 macaroon="%s", invoice="%s", token_id="%s"`,
			mac, invoice, tok.TokenID))
	writeJSON(w, http.StatusPaymentRequired, map[string]any{
		"code":         "payment_required",
		"token_id":     tok.TokenID,
		"satoshi_price": h.SatoshiPrice,
		"invoice":      invoice,
	})
}

// ─── GET /v1/protocol/verify/{token_id} ──────────────────────────────────────

// consumeToken handles GET /v1/protocol/verify/{token_id}.
//
// In stub mode, payment is not verified — the token is consumed and the
// proof is returned immediately. In production Phase 2 this endpoint
// would verify the Lightning pre-image before consuming.
func (h *Handler) consumeToken(w http.ResponseWriter, r *http.Request) {
	rawID := chi.URLParam(r, "token_id")
	tokenID, err := uuid.Parse(rawID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_token_id",
			"token_id must be a valid UUID")
		return
	}
	h.consumeAndRespond(w, r, tokenID)
}

// consumeAndRespond is the shared path used by both endpoints.
func (h *Handler) consumeAndRespond(w http.ResponseWriter, r *http.Request, tokenID uuid.UUID) {
	tok, err := h.Store.GetToken(r.Context(), tokenID)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "token_not_found", "")
		return
	case err != nil:
		h.Logger.Error("consumeAndRespond get_token", zap.Stringer("token_id", tokenID), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "lookup_failed", "")
		return
	}

	switch tok.Status {
	case "consumed":
		writeError(w, http.StatusGone, "token_consumed", "token has already been used")
		return
	case "expired":
		writeError(w, http.StatusGone, "token_expired", "token has expired")
		return
	}

	// Fetch the proof before consuming so we never consume without a proof.
	proof, err := h.Store.GetAnchorProof(r.Context(), tok.EventHash)
	switch {
	case errors.Is(err, ErrNotAnchored):
		writeJSON(w, http.StatusOK, map[string]any{
			"verified":      false,
			"anchor_status": "pending",
			"message":       "event not yet anchored",
			"event_hash":    tok.EventHash,
		})
		return
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "event_not_found", "")
		return
	case err != nil:
		h.Logger.Error("consumeAndRespond get_anchor_proof",
			zap.String("event_hash", tok.EventHash), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "proof_lookup_failed", "")
		return
	}

	if err := h.Store.ConsumeToken(r.Context(), tokenID); err != nil {
		switch {
		case errors.Is(err, ErrAlreadyConsumed):
			writeError(w, http.StatusGone, "token_consumed", "token has already been used")
		case errors.Is(err, ErrExpired):
			writeError(w, http.StatusGone, "token_expired", "token has expired")
		default:
			h.Logger.Error("consumeAndRespond consume_token",
				zap.Stringer("token_id", tokenID), zap.Error(err))
			writeError(w, http.StatusInternalServerError, "consume_failed", "")
		}
		return
	}

	// Parse the Merkle proof JSON from the DB.
	var proofNodes []sub3.ProofNode
	if len(proof.MerkleProof) > 0 {
		if err := json.Unmarshal(proof.MerkleProof, &proofNodes); err != nil {
			h.Logger.Warn("consumeAndRespond unmarshal_proof",
				zap.String("event_hash", tok.EventHash), zap.Error(err))
		}
	}

	verified := sub3.VerifyProof(proof.MerkleRoot, proof.EventHash, proofNodes)

	resp := VerificationResponse{
		EventHash:      proof.EventHash,
		AnchorID:       proof.AnchorID,
		MerkleRoot:     proof.MerkleRoot,
		InscriptionID:  proof.InscriptionID,
		BtcTxID:        proof.BtcTxID,
		BtcBlockHeight: proof.BtcBlockHeight,
		Network:        proof.Network,
		AnchorStatus:   proof.AnchorStatus,
		LeafIndex:      proof.LeafIndex,
		MerkleProof:    proofNodes,
		AnchoredAt:     proof.AnchoredAt,
		Verified:       verified,
		SatoshiPrice:   tok.SatoshiPrice,
	}
	writeJSON(w, http.StatusOK, resp)
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// isValidEventHash returns true iff s is a 64-character lowercase hex string.
func isValidEventHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// parseL402Header extracts the token_id and macaroon from an
// "Authorization: L402 ..." header value.
//
// Expected format (from WWW-Authenticate we issued):
//
//	L402 macaroon="<hex>", invoice="<stub>", token_id="<uuid>"
//
// or the minimal form a client might send back:
//
//	L402 <macaroon>:<preimage>
//
// We support both forms. The token_id must be present somewhere.
func parseL402Header(header string) (tokenID uuid.UUID, macaroon string, ok bool) {
	body := strings.TrimPrefix(header, "L402 ")

	// Try structured form: macaroon="...", token_id="..."
	params := parseQuotedParams(body)
	if mac, hasMac := params["macaroon"]; hasMac {
		if tidStr, hasTID := params["token_id"]; hasTID {
			tid, err := uuid.Parse(tidStr)
			if err == nil {
				return tid, mac, true
			}
		}
	}
	return uuid.UUID{}, "", false
}

// parseQuotedParams parses a comma-separated list of key="value" pairs.
func parseQuotedParams(s string) map[string]string {
	out := make(map[string]string)
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.Trim(strings.TrimSpace(kv[1]), `"`)
		out[key] = val
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, detail string) {
	body := map[string]string{"code": code}
	if detail != "" {
		body["error"] = detail
	}
	writeJSON(w, status, body)
}
