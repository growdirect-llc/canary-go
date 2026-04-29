// cmd/identity/handlers.go
package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/growdirect-llc/rapidpos/internal/auth"
	"github.com/growdirect-llc/rapidpos/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type handlers struct {
	pool *pgxpool.Pool
	rdb  *redis.Client
	cfg  *config.Config
}

type healthResponse struct {
	OK      bool              `json:"ok"`
	Service string            `json:"service"`
	Version string            `json:"version"`
	Checks  map[string]string `json:"checks"`
}

func (h *handlers) health(w http.ResponseWriter, r *http.Request) {
	checks := map[string]string{
		"database": "ok",
		"valkey":   "ok",
	}
	statusCode := http.StatusOK

	if err := h.pool.Ping(r.Context()); err != nil {
		checks["database"] = "error: " + err.Error()
		statusCode = http.StatusServiceUnavailable
	}
	if err := h.rdb.Ping(r.Context()).Err(); err != nil {
		checks["valkey"] = "error: " + err.Error()
		statusCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(healthResponse{
		OK:      statusCode == http.StatusOK,
		Service: h.cfg.ServiceName,
		Version: "1.0.0",
		Checks:  checks,
	})
}

type validateRequest struct {
	Token string `json:"token"`
}

type validateResponse struct {
	Valid      bool     `json:"valid"`
	MerchantID string   `json:"merchant_id,omitempty"`
	UserID     string   `json:"user_id,omitempty"`
	Roles      []string `json:"roles,omitempty"`
}

func (h *handlers) sessionsValidate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req validateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(validateResponse{Valid: false})
		return
	}

	claims, err := auth.VerifyToken(h.cfg.SessionSecret, req.Token)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(validateResponse{Valid: false})
		return
	}

	// Revocation check — token must exist in Valkey
	key := fmt.Sprintf("session:%x", sha256.Sum256([]byte(req.Token)))
	if exists, err := h.rdb.Exists(r.Context(), key).Result(); err != nil || exists == 0 {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(validateResponse{Valid: false})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(validateResponse{
		Valid:      true,
		MerchantID: claims.MerchantID.String(),
		UserID:     claims.UserID.String(),
		Roles:      claims.Roles,
	})
}
