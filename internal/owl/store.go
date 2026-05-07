package owl

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruptiv/canary/internal/owl/metrics"
)

// Store is the read-only data access surface for Owl. All methods take
// a (tenant, period) tuple — tenant resolved upstream from merchant_id.
//
// Loop 2 dispatch overrides the CanaryGo CLAUDE.md "no raw SQL" rule —
// Owl is allowed direct pgx + raw SQL because the dashboard surface is
// fluid and the sqlc retrofit happens in Loop 3.
type Store interface {
	// ResolveMerchant turns a merchant_id into (tenant_id, IANA timezone).
	// Returns ErrMerchantNotFound if the merchant doesn't exist.
	// Timezone defaults to "UTC" when no merchant_settings row exists.
	ResolveMerchant(ctx context.Context, merchantID uuid.UUID) (tenantID uuid.UUID, timezone string, err error)

	SalesSummary(ctx context.Context, tenantID uuid.UUID, p Period) (SalesSummary, error)
	TopItemsByUnits(ctx context.Context, tenantID uuid.UUID, p Period, limit int) ([]ItemMetric, error)
	TopItemsByRevenue(ctx context.Context, tenantID uuid.UUID, p Period, limit int) ([]ItemMetric, error)
	UnknownItemCount(ctx context.Context, tenantID uuid.UUID, p Period) (int64, error)
	SalesByLocation(ctx context.Context, tenantID uuid.UUID, p Period) ([]LocationMetric, error)
	CasesSummary(ctx context.Context, tenantID uuid.UUID, p Period) (CasesSummary, error)
	DetectionRate(ctx context.Context, tenantID uuid.UUID, p Period) (DetectionRate, error)
	CashierExposure(ctx context.Context, tenantID uuid.UUID, p Period, limit int) ([]CashierExposure, error)
}

// ErrMerchantNotFound is returned by ResolveMerchant when the merchant
// doesn't exist or has no tenant link.
var ErrMerchantNotFound = errors.New("owl: merchant not found")

// PgxStore is the pgxpool-backed Store implementation. It delegates
// each metric method to the helpers in internal/owl/metrics — those
// own the SQL.
type PgxStore struct {
	pool *pgxpool.Pool
}

// NewPgxStore wires a Store over an existing pgxpool.Pool. Caller owns
// the pool's lifecycle.
func NewPgxStore(pool *pgxpool.Pool) *PgxStore {
	return &PgxStore{pool: pool}
}

// ──────────────────────────────────────────────────────────────────────
// ResolveMerchant
// ──────────────────────────────────────────────────────────────────────

// ResolveMerchant fetches tenant_id + timezone for a merchant.
//
// SDD-conflict: owl.md SDD assumes "merchant_id" is the tenant key
// throughout queries. The canonical schema FKs everything to
// app.tenants(id) — merchant_id only lives on app.merchants and the
// legacy app.* (locations, employees, users) tables. We resolve once
// at the top of every dashboard request and pass tenant_id thereafter.
//
// SDD-missing: there is no FK constraint requiring app.merchants.tenant_id
// to be non-null. The column was added "nullable during transition;
// backfill in seed" (01_app_foundation.sql:59). Owl treats a NULL
// tenant_id as "merchant exists but isn't onboarded yet" → returns
// ErrMerchantNotFound rather than guessing.
func (s *PgxStore) ResolveMerchant(ctx context.Context, merchantID uuid.UUID) (uuid.UUID, string, error) {
	const q = `
		SELECT m.tenant_id, COALESCE(ms.timezone, 'UTC')
		FROM app.merchants m
		LEFT JOIN app.merchant_settings ms ON ms.merchant_id = m.id
		WHERE m.id = $1
	`
	var (
		tenantID *uuid.UUID
		tz       string
	)
	if err := s.pool.QueryRow(ctx, q, merchantID).Scan(&tenantID, &tz); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, "", ErrMerchantNotFound
		}
		return uuid.Nil, "", fmt.Errorf("owl: resolve merchant: %w", err)
	}
	if tenantID == nil {
		return uuid.Nil, "", ErrMerchantNotFound
	}
	return *tenantID, tz, nil
}

// ──────────────────────────────────────────────────────────────────────
// Metric delegates — SQL lives in internal/owl/metrics
// ──────────────────────────────────────────────────────────────────────

func (s *PgxStore) SalesSummary(ctx context.Context, tenantID uuid.UUID, p Period) (SalesSummary, error) {
	return metrics.SalesSummary(ctx, s.pool, tenantID, p.From, p.To)
}

func (s *PgxStore) TopItemsByUnits(ctx context.Context, tenantID uuid.UUID, p Period, limit int) ([]ItemMetric, error) {
	return metrics.TopItemsByUnits(ctx, s.pool, tenantID, p.From, p.To, limit)
}

func (s *PgxStore) TopItemsByRevenue(ctx context.Context, tenantID uuid.UUID, p Period, limit int) ([]ItemMetric, error) {
	return metrics.TopItemsByRevenue(ctx, s.pool, tenantID, p.From, p.To, limit)
}

func (s *PgxStore) UnknownItemCount(ctx context.Context, tenantID uuid.UUID, p Period) (int64, error) {
	return metrics.UnknownItemCount(ctx, s.pool, tenantID, p.From, p.To)
}

func (s *PgxStore) SalesByLocation(ctx context.Context, tenantID uuid.UUID, p Period) ([]LocationMetric, error) {
	return metrics.SalesByLocation(ctx, s.pool, tenantID, p.From, p.To)
}

func (s *PgxStore) CasesSummary(ctx context.Context, tenantID uuid.UUID, p Period) (CasesSummary, error) {
	return metrics.CasesSummary(ctx, s.pool, tenantID, p.From, p.To)
}

func (s *PgxStore) DetectionRate(ctx context.Context, tenantID uuid.UUID, p Period) (DetectionRate, error) {
	return metrics.DetectionRate(ctx, s.pool, tenantID, p.From, p.To)
}

func (s *PgxStore) CashierExposure(ctx context.Context, tenantID uuid.UUID, p Period, limit int) ([]CashierExposure, error) {
	return metrics.CashierExposure(ctx, s.pool, tenantID, p.From, p.To, limit)
}
