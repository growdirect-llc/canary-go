// Package jwks serves the public JWKS document at
// /.well-known/jwks.json — the public-key set callers (AtlasView's
// JWT middleware, third-party verifiers) consume to validate JWTs
// minted by canary.go's identity service.
//
// Per RFC 7517 §5 the response shape is `{"keys":[<jwk>, <jwk>...]}`.
// We publish active + retiring keys; expired keys are excluded.
//
// T-2 / GRO-862 Phase 4.
package jwks

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/identity/keystore"
)

// VerifySetReader is the keystore surface the handler depends on.
// Held as an interface so tests can stub the keystore without a
// pgx pool. *keystore.Store satisfies it.
type VerifySetReader interface {
	VerifySet(ctx context.Context) ([]keystore.SigningKey, error)
}

// Handler emits the JWKS document.
type Handler struct {
	store  VerifySetReader
	logger *zap.Logger
}

// New wires a handler. Logger may be nil (no-op fallback).
func New(store VerifySetReader, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{store: store, logger: logger}
}

// Mount registers the well-known JWKS endpoint on r.
//
// `/.well-known/jwks.json` is the IANA-registered location per
// RFC 5785 + RFC 7517. AtlasView caches by URL; serving from any
// other path means external consumers won't find it without
// configuration.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/.well-known/jwks.json", h.serve)
}

// serve emits `{"keys":[<jwk>, ...]}`. Cache-Control caps client
// caching at 60s, matching the keystore TTL — JWKS clients commonly
// re-fetch on key-not-found, so a short cache lets rotation
// propagate within roughly one minute end-to-end (TTL on canary side
// + TTL on AtlasView side).
func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	set, err := h.store.VerifySet(r.Context())
	if err != nil {
		h.logger.Error("jwks verify-set", zap.Error(err))
		http.Error(w, `{"error":"jwks_unavailable"}`, http.StatusInternalServerError)
		return
	}

	// keystore.SigningKey.PublicJWK is already raw JWK JSON. We wrap
	// the slice in the {"keys":...} envelope. json.RawMessage avoids
	// re-marshaling each JWK and preserves the byte-stable encoding
	// the keystore generated.
	keys := make([]json.RawMessage, 0, len(set))
	for _, k := range set {
		if len(k.PublicJWK) == 0 {
			// Defensive — a malformed row shouldn't take down the
			// whole JWKS. Log and skip.
			h.logger.Warn("jwks: skipping key with empty PublicJWK",
				zap.String("kid", k.Kid),
				zap.String("status", k.Status),
			)
			continue
		}
		keys = append(keys, k.PublicJWK)
	}

	body, err := json.Marshal(struct {
		Keys []json.RawMessage `json:"keys"`
	}{Keys: keys})
	if err != nil {
		h.logger.Error("jwks marshal", zap.Error(err))
		http.Error(w, `{"error":"jwks_unavailable"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/jwk-set+json")
	w.Header().Set("Cache-Control", "public, max-age=60")
	_, _ = w.Write(body)
}
