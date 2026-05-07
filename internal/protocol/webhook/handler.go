// Package webhook implements the gateway's POST handler for inbound
// source-network events. It is the runtime embodiment of patent
// Application 63/991,596, Node 2 (API Gateway).
//
// Flow per request:
//
//  1. Parse path/headers → source_code, merchant_id, signature
//  2. Read raw body (capped by maxBodyBytes)
//  3. Resolve per-(merchant, source) secret + replay window
//  4. Verify HMAC-SHA256 + timestamp window + nonce single-use
//  5. Compute SHA-256(payload) → event_hash
//  6. Mint UUIDv4 → event_id
//  7. Publish canonical Event onto Valkey Streams
//  8. Return 200 OK with the assigned event_id + event_hash
//
// SLO: p99 < 5ms end-to-end at production load.
package webhook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/growdirect-llc/rapidpos/internal/protocol/audit"
	canaryhmac "github.com/growdirect-llc/rapidpos/internal/protocol/hmac"
	"github.com/growdirect-llc/rapidpos/internal/protocol/publisher"
	"github.com/growdirect-llc/rapidpos/internal/protocol/secrets"
)

// HeaderMerchant carries the tenant scope. Sources that don't natively
// know our merchant UUID get a per-source thin proxy in front of the
// gateway that adds this header from configuration.
const HeaderMerchant = "X-Canary-Merchant"

// MaxBodyBytes caps the raw payload size accepted by the gateway.
// Webhooks above this are rejected with 413; anything legitimate fits.
const MaxBodyBytes = 1 << 20 // 1 MiB

// Handler is the chi-compatible handler factory. Inject the resolver,
// publisher, optional nonce store, and logger; receive the http.Handler
// to mount.
type Handler struct {
	Resolver  secrets.Resolver
	Publisher publisher.Publisher
	Nonces    canaryhmac.NonceStore // may be nil — falls back to timestamp-only
	Logger    *zap.Logger
	Now       func() time.Time // overridable for tests
}

// New constructs a Handler with sensible defaults.
func New(r secrets.Resolver, p publisher.Publisher, n canaryhmac.NonceStore, l *zap.Logger) *Handler {
	if l == nil {
		l = zap.NewNop()
	}
	return &Handler{
		Resolver:  r,
		Publisher: p,
		Nonces:    n,
		Logger:    l,
		Now:       func() time.Time { return time.Now().UTC() },
	}
}

// Mount registers the webhook route on a chi router. Path:
// POST /v1/protocol/webhook/{source}
func (h *Handler) Mount(r chi.Router) {
	r.Post("/v1/protocol/webhook/{source}", h.ServeHTTP)
}

// Response is the JSON success body returned to the source network.
type Response struct {
	EventID   string `json:"event_id"`
	EventHash string `json:"event_hash"`
	Status    string `json:"status"`
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := h.Logger.With(zap.String("op", "webhook.ingest"))

	source := chi.URLParam(r, "source")
	if source == "" {
		writeError(w, http.StatusBadRequest, "missing_source", "URL must include a source code")
		return
	}

	merchantHeader := r.Header.Get(HeaderMerchant)
	if merchantHeader == "" {
		writeError(w, http.StatusBadRequest, "missing_merchant",
			fmt.Sprintf("header %s is required", HeaderMerchant))
		return
	}
	merchantID, err := uuid.Parse(merchantHeader)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_merchant",
			"X-Canary-Merchant must be a UUID")
		return
	}

	// Read the body with a hard cap so a malicious source can't blow our heap.
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		// MaxBytesReader translates an over-cap read into an error during
		// ReadAll; chi+net/http handle the response code via the writer.
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large",
				fmt.Sprintf("max body is %d bytes", MaxBodyBytes))
			return
		}
		writeError(w, http.StatusBadRequest, "body_read_failed", err.Error())
		return
	}

	// Look up the per-(merchant, source) secret.
	sec, err := h.Resolver.Lookup(ctx, merchantID, source)
	if err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			// 401 — don't reveal whether merchant or source is unknown
			writeError(w, http.StatusUnauthorized, "unknown_source", "")
			return
		}
		logger.Error("secrets lookup failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "secrets_lookup_failed", "")
		return
	}

	// Parse the signature headers.
	sig, err := canaryhmac.ParseHeaders(r.Header.Get)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_signature_headers", err.Error())
		return
	}

	// Verify HMAC + timestamp + nonce.
	verifier := canaryhmac.New(sec.Secret, sec.ReplayWindow, h.Nonces)
	if err := verifier.Verify(ctx, payload, sig, h.Now()); err != nil {
		// Map verification errors to status codes — all are 401 except
		// malformed timestamp/signature which are 400.
		switch {
		case errors.Is(err, canaryhmac.ErrTimestampMalformed),
			errors.Is(err, canaryhmac.ErrSignatureMalformed):
			writeError(w, http.StatusBadRequest, "signature_malformed", err.Error())
		case errors.Is(err, canaryhmac.ErrTimestampMissing),
			errors.Is(err, canaryhmac.ErrSignatureMissing),
			errors.Is(err, canaryhmac.ErrNonceMissing),
			errors.Is(err, canaryhmac.ErrTimestampOutOfWindow),
			errors.Is(err, canaryhmac.ErrSignatureMismatch),
			errors.Is(err, canaryhmac.ErrNonceReplay):
			writeError(w, http.StatusUnauthorized, "signature_invalid", "")
		default:
			logger.Warn("signature verification error", zap.Error(err))
			writeError(w, http.StatusUnauthorized, "signature_invalid", "")
		}
		return
	}

	// Compute the canonical event_hash (sha256 hex of raw payload bytes).
	hashBytes := sha256.Sum256(payload)
	eventHash := hex.EncodeToString(hashBytes[:])

	// Mint event_id and build the canonical envelope.
	eventID := uuid.New()

	// Bridge minted event_id + resolved source code onto the request
	// context so the audit middleware (GRO-694) can record them. This
	// is a no-op when the middleware isn't installed.
	ctx = audit.WithEventID(ctx, eventID)
	ctx = audit.WithSource(ctx, source)
	*r = *r.WithContext(ctx)

	evt := publisher.Event{
		EventID:    eventID,
		EventHash:  eventHash,
		SourceCode: source,
		MerchantID: merchantID,
		Timestamp:  sig.Timestamp,
		IngestedAt: h.Now(),
		Payload:    json.RawMessage(payload),
		Nonce:      sig.Nonce,
	}

	if err := h.Publisher.Publish(ctx, evt); err != nil {
		logger.Error("publish failed", zap.Error(err), zap.String("event_id", eventID.String()))
		// 5xx so the source retries — the event was authentic, our infra failed.
		writeError(w, http.StatusInternalServerError, "publish_failed", "")
		return
	}

	logger.Debug("webhook accepted",
		zap.String("source", source),
		zap.String("merchant_id", merchantID.String()),
		zap.String("event_id", eventID.String()),
		zap.String("event_hash", eventHash),
	)

	writeJSON(w, http.StatusOK, Response{
		EventID:   eventID.String(),
		EventHash: eventHash,
		Status:    "accepted",
	})
}

// errorBody is the JSON shape returned on every non-2xx response.
type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Code: code, Message: message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Compile-time guard: make sure context import is needed (silences
// linter when ctx isn't referenced explicitly outside of derivation).
var _ = context.Background
