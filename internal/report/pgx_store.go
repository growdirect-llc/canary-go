// internal/report/pgx_store.go
package report

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxStore is the pgx-backed report job store.
type PgxStore struct {
	pool *pgxpool.Pool
}

func NewPgxStore(pool *pgxpool.Pool) *PgxStore {
	return &PgxStore{pool: pool}
}

func (s *PgxStore) Create(ctx context.Context, tenantID uuid.UUID, req ReportRequest) (*ReportJob, error) {
	if req.Format == "" {
		req.Format = "csv"
	}
	now := time.Now().UTC()
	const q = `
		INSERT INTO app.report_jobs
		       (tenant_id, report_type, status, format, date_from, date_to, location_id, created_at, updated_at)
		VALUES ($1, $2, 'pending', $3, $4, $5, $6, $7, $7)
		RETURNING id, tenant_id, report_type, status, format, date_from, date_to,
		          location_id, download_url, error_msg, created_at, updated_at`
	row := s.pool.QueryRow(ctx, q,
		tenantID, string(req.ReportType), req.Format,
		req.From, req.To, req.LocationID, now,
	)
	return scanJob(row)
}

func (s *PgxStore) GetByID(ctx context.Context, tenantID, jobID uuid.UUID) (*ReportJob, error) {
	const q = `
		SELECT id, tenant_id, report_type, status, format, date_from, date_to,
		       location_id, download_url, error_msg, created_at, updated_at
		  FROM app.report_jobs
		 WHERE tenant_id = $1 AND id = $2`
	row := s.pool.QueryRow(ctx, q, tenantID, jobID)
	j, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return j, err
}

func (s *PgxStore) List(ctx context.Context, tenantID uuid.UUID) ([]*ReportJob, error) {
	const q = `
		SELECT id, tenant_id, report_type, status, format, date_from, date_to,
		       location_id, download_url, error_msg, created_at, updated_at
		  FROM app.report_jobs
		 WHERE tenant_id = $1
		 ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("report: list: %w", err)
	}
	defer rows.Close()
	var out []*ReportJob
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func scanJob(row interface{ Scan(dest ...any) error }) (*ReportJob, error) {
	var j ReportJob
	var locID *uuid.UUID
	var downloadURL, errorMsg *string
	var dateFrom, dateTo *string
	if err := row.Scan(
		&j.JobID, &j.TenantID, &j.ReportType, &j.Status, &j.Format,
		&dateFrom, &dateTo, &locID, &downloadURL, &errorMsg,
		&j.CreatedAt, &j.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if dateFrom != nil {
		j.From = *dateFrom
	}
	if dateTo != nil {
		j.To = *dateTo
	}
	j.DownloadURL = downloadURL
	j.Error = errorMsg
	return &j, nil
}
