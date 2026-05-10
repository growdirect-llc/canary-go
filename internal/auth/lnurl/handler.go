package lnurl

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

	// bech32 — https://github.com/btcsuite/btcd/tree/master/btcutil/bech32
	"github.com/btcsuite/btcd/btcutil/bech32"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Handler exposes the LNURL-auth login surface.
//
//	GET /v1/auth/lnurl           — generate QR challenge
//	GET /v1/auth/lnurl/challenge — wallet handshake endpoint
//	GET /v1/auth/lnurl/callback  — wallet callback (sig verify + JWT)
//	GET /v1/auth/session         — UI polls for session token
type Handler struct {
	Store  LNURLStore
	Secret []byte   // LNURL_JWT_SECRET raw bytes
	Stub   bool     // LNURL_STUB=true skips secp256k1 verification
	Scheme string   // "http" or "https"; set from LNURL_SCHEME env var
	Host   string   // set from LNURL_HOST env var
	Logger *zap.Logger

	// pendingTokens maps k1 → JWT string for the poll endpoint to consume.
	// Known limitation: no TTL — tokens from abandoned sessions (UI closed before
	// polling) accumulate until process restart. Schedule cleanup in a follow-up
	// wave when session load warrants it.
	pendingTokens sync.Map
}

// NewHandler constructs a Handler, building the PgxStore internally.
func NewHandler(pool *pgxpool.Pool, secret []byte, stub bool, scheme, host string, logger *zap.Logger) *Handler {
	return &Handler{
		Store:  NewStore(pool),
		Secret: secret,
		Stub:   stub,
		Scheme: scheme,
		Host:   host,
		Logger: logger,
	}
}

// Mount registers all four LNURL-auth routes on a chi router.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/v1/auth/lnurl", h.getLNURL)
	r.Get("/v1/auth/lnurl/challenge", h.challenge)
	r.Get("/v1/auth/lnurl/callback", h.callback)
	r.Get("/v1/auth/session", h.pollSession)
}

// ─── GET /v1/auth/lnurl ──────────────────────────────────────────────────────

// getLNURL generates a fresh k1, stores it, bech32-encodes the callback URL,
// and returns {"lnurl": "lnurl1...", "k1": "<k1>"}.
func (h *Handler) getLNURL(w http.ResponseWriter, r *http.Request) {
	// Generate 32 random bytes for the challenge.
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		h.Logger.Error("lnurl get_lnurl rand", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "rand_failed", "")
		return
	}
	k1 := hex.EncodeToString(raw)

	if err := h.Store.InsertChallenge(r.Context(), k1); err != nil {
		h.Logger.Error("lnurl get_lnurl insert_challenge", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "store_failed", "")
		return
	}

	callbackURL := h.callbackURL(k1)

	lnurlStr, err := encodeLNURL(callbackURL)
	if err != nil {
		h.Logger.Error("lnurl get_lnurl encode", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "encode_failed", "")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"lnurl": lnurlStr,
		"k1":    k1,
	})
}

// ─── GET /v1/auth/lnurl/challenge ────────────────────────────────────────────

// challenge is the endpoint wallets hit after decoding the LNURL QR.
// Returns the login parameters per the LNURL-auth spec.
func (h *Handler) challenge(w http.ResponseWriter, r *http.Request) {
	k1 := r.URL.Query().Get("k1")
	if k1 == "" {
		writeError(w, http.StatusBadRequest, "missing_k1", "k1 query param required")
		return
	}

	c, err := h.Store.GetChallenge(r.Context(), k1)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "k1 not found or expired")
		return
	case err != nil:
		h.Logger.Error("lnurl challenge get", zap.String("k1", k1), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "lookup_failed", "")
		return
	}

	if c.Status != "pending" {
		writeError(w, http.StatusNotFound, "not_found", "k1 not found or expired")
		return
	}

	callbackURL := fmt.Sprintf("%s://%s/v1/auth/lnurl/callback", h.Scheme, h.Host)

	writeJSON(w, http.StatusOK, map[string]string{
		"tag":      "login",
		"k1":       k1,
		"action":   "auth",
		"callback": callbackURL,
	})
}

// ─── GET /v1/auth/lnurl/callback ─────────────────────────────────────────────

// callback is what the wallet calls after the user approves login.
// Query params: tag=login&k1=<k1>&sig=<DER-sig-hex>&key=<compressed-pubkey-hex>
func (h *Handler) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	k1 := q.Get("k1")
	sig := q.Get("sig")
	key := q.Get("key")

	if k1 == "" || sig == "" || key == "" {
		writeError(w, http.StatusBadRequest, "missing_params", "k1, sig, and key are required")
		return
	}

	// Signature verification (skipped in stub mode for CI/signet).
	if !h.Stub {
		valid, err := VerifySignature(k1, sig, key)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_params", err.Error())
			return
		}
		if !valid {
			writeError(w, http.StatusBadRequest, "invalid_signature", "signature does not verify")
			return
		}
	}

	// Verify challenge is consumable before writing key association.
	ownerID := deterministicOwnerID(key)
	if err := h.Store.MarkUsed(r.Context(), k1, ownerID); err != nil {
		switch {
		case errors.Is(err, ErrAlreadyUsed):
			writeError(w, http.StatusConflict, "already_used", "challenge already used")
		case errors.Is(err, ErrExpired):
			writeError(w, http.StatusGone, "expired", "challenge expired")
		default:
			h.Logger.Error("lnurl: mark used", zap.Error(err))
			writeError(w, http.StatusInternalServerError, "internal_error", "")
		}
		return
	}
	if err := h.Store.UpsertLinkedKey(r.Context(), key, ownerID); err != nil {
		h.Logger.Error("lnurl: upsert linked key", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "internal_error", "")
		return
	}

	jwtStr, err := IssueJWT(ownerID, key, h.Secret)
	if err != nil {
		h.Logger.Error("lnurl callback issue_jwt", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "jwt_failed", "")
		return
	}

	// Park the JWT in-memory so pollSession can hand it to the UI.
	h.pendingTokens.Store(k1, jwtStr)

	writeJSON(w, http.StatusOK, map[string]string{"status": "OK"})
}

// ─── GET /v1/auth/session ────────────────────────────────────────────────────

// pollSession is the UI's polling endpoint.
// Query param: k1=<k1>
// Returns {"status":"ok","token":"<jwt>"} (200) when ready, or
//
//	{"status":"pending"} (202) while waiting.
func (h *Handler) pollSession(w http.ResponseWriter, r *http.Request) {
	k1 := r.URL.Query().Get("k1")
	if k1 == "" {
		writeError(w, http.StatusBadRequest, "missing_k1", "k1 query param required")
		return
	}

	val, ok := h.pendingTokens.LoadAndDelete(k1)
	if !ok {
		// Check if k1 is known at all; distinguish pending vs expired/used.
		c, err := h.Store.GetChallenge(r.Context(), k1)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeError(w, http.StatusGone, "not_found", "k1 not found or expired")
			} else {
				writeError(w, http.StatusInternalServerError, "lookup_failed", "")
			}
			return
		}
		if c.Status == "used" || c.Status == "expired" {
			writeError(w, http.StatusGone, "expired_or_used", "challenge has expired or been used")
			return
		}
		// Still pending — wallet hasn't responded yet.
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending"})
		return
	}

	jwtStr, _ := val.(string)
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"token":  jwtStr,
	})
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// lnurlNamespace is the fixed UUID v5 namespace for LNURL linking keys.
// Must not change after deployment — changing it invalidates all existing owner IDs.
var lnurlNamespace = uuid.MustParse("6ba7b814-9dad-11d1-80b4-00c04fd430c8") // uuid.NamespaceDNS

func deterministicOwnerID(linkingKey string) uuid.UUID {
	return uuid.NewSHA1(lnurlNamespace, []byte(linkingKey))
}

// callbackURL builds the full callback URL for a given k1 challenge.
func (h *Handler) callbackURL(k1 string) string {
	return fmt.Sprintf("%s://%s/v1/auth/lnurl/callback?tag=login&k1=%s", h.Scheme, h.Host, k1)
}

// encodeLNURL bech32-encodes a URL as an LNURL per the spec:
// bytes of the URL → 5-bit groups → bech32("lnurl", groups).
//
// Uses github.com/btcsuite/btcutil/bech32 (a transitive dep of btcec).
func encodeLNURL(callbackURL string) (string, error) {
	data := []byte(callbackURL)
	// Convert 8-bit groups to 5-bit groups required by bech32.
	converted, err := bech32.ConvertBits(data, 8, 5, true)
	if err != nil {
		return "", fmt.Errorf("lnurl encode convert_bits: %w", err)
	}
	encoded, err := bech32.Encode("lnurl", converted)
	if err != nil {
		return "", fmt.Errorf("lnurl encode bech32: %w", err)
	}
	return encoded, nil
}

// ─── HTTP utilities ───────────────────────────────────────────────────────────

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorBody{Code: code, Message: msg})
}
