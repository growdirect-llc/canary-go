package namespace

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/identity"
	"github.com/ruptiv/canary/internal/protocol/sub3"
)

// Handler exposes the .jeffe namespace registration and lookup API.
//
//	POST /v1/protocol/namespace        — register a name (auth-required)
//	GET  /v1/protocol/namespace/{name} — look up a registration (public)
//
// T-C / GRO-849 splits the surface: lookups stay public (the namespace
// is intentionally a public registry), but registration requires an
// API key whose tenant matches the request's owner_id — preventing
// spoofed registrations that claim ownership of someone else's
// identity. Use MountPublic + MountProtected from the gateway to
// place POST behind APIKeyMiddleware while leaving GET reachable.
type Handler struct {
	store     NamespaceStore
	inscriber sub3.Inscriber
	logger    *zap.Logger
}

// New wires a Handler. Logger may be nil.
func New(pool *pgxpool.Pool, inscriber sub3.Inscriber, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{
		store:     NewStore(pool),
		inscriber: inscriber,
		logger:    logger,
	}
}

// Mount keeps the legacy single-router wiring for tests that don't
// exercise auth. Production wiring (cmd/gateway/main.go) calls
// MountPublic + MountProtected separately so POST sits behind
// APIKeyMiddleware.
func (h *Handler) Mount(r chi.Router) {
	h.MountProtected(r)
	h.MountPublic(r)
}

// MountPublic registers the read-only lookup. Reachable without an
// API key — the namespace registry is intentionally world-readable.
func (h *Handler) MountPublic(r chi.Router) {
	r.Get("/v1/protocol/namespace/{name}", h.handleLookup)
}

// MountProtected registers the POST register endpoint. Caller must
// place this inside a Group that runs APIKeyMiddleware so the
// handler sees identity claims and can enforce tenant ownership.
func (h *Handler) MountProtected(r chi.Router) {
	r.Post("/v1/protocol/namespace", h.handleRegister)
}

// ─── POST /v1/protocol/namespace ─────────────────────────────────────────────

type registerRequest struct {
	Name      string `json:"name"`
	OwnerID   string `json:"owner_id"`
	OwnerType string `json:"owner_type"`
	Network   string `json:"network"`
}

func (h *Handler) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code":  "invalid_json",
			"error": err.Error(),
		})
		return
	}

	// Validate required fields.
	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "missing_name",
		})
		return
	}
	ownerID, err := uuid.Parse(req.OwnerID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code":  "invalid_owner_id",
			"error": "owner_id must be a valid UUID",
		})
		return
	}
	if req.OwnerType == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "missing_owner_type",
		})
		return
	}

	// T-C / GRO-849: ownership proof. Caller must present an API key
	// (claims attached by APIKeyMiddleware). Tenant-scoped keys can
	// register names only against their own tenant UUID — a tenant-
	// scoped caller passing a different owner_id would otherwise be
	// claiming ownership of someone else's identity. Platform-scope
	// keys (claims.TenantID == uuid.Nil) are exempt — those are
	// admin-controlled and may register any owner.
	claims, ok := identity.ClaimsFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"code":  "unauthenticated",
			"error": "POST /v1/protocol/namespace requires an API key",
		})
		return
	}
	if claims.TenantID != uuid.Nil && claims.TenantID != ownerID {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"code":  "owner_mismatch",
			"error": "owner_id does not match the authenticated tenant",
		})
		return
	}

	network := req.Network
	if network == "" {
		network = "signet"
	}

	reg, err := Register(r.Context(), h.store, h.inscriber, RegisterRequest{
		Name:      req.Name,
		OwnerID:   ownerID,
		OwnerType: req.OwnerType,
		Network:   network,
	})

	switch {
	case errors.Is(err, ErrInvalidName):
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code":  "invalid_name",
			"error": err.Error(),
		})
		return
	case errors.Is(err, ErrNameTaken):
		writeJSON(w, http.StatusConflict, map[string]string{
			"code":  "name_taken",
			"error": "name is already registered",
			"name":  req.Name,
		})
		return
	case err != nil:
		h.logger.Error("namespace register", zap.String("name", req.Name), zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "registration_failed",
		})
		return
	}

	writeJSON(w, http.StatusCreated, reg)
}

// ─── GET /v1/protocol/namespace/{name} ───────────────────────────────────────

func (h *Handler) handleLookup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "missing_name",
		})
		return
	}

	reg, err := h.store.GetByName(r.Context(), name)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeJSON(w, http.StatusNotFound, map[string]string{
			"code": "not_found",
			"name": name,
		})
		return
	case err != nil:
		h.logger.Error("namespace lookup", zap.String("name", name), zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "lookup_failed",
		})
		return
	}

	writeJSON(w, http.StatusOK, reg)
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
