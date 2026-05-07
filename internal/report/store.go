// internal/report/store.go
//
// In-memory job store for report generation requests. A persistent
// job table (app.report_jobs) is scoped to Wave E schema work. This
// stub keeps the API contract live and testable without a DB migration.
//
// Spec: GRO-766 Phase E.

package report

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("report: job not found")

// Store holds report jobs in memory. Safe for concurrent use.
// Replace with a pgx-backed store when app.report_jobs is migrated.
type Store struct {
	mu   sync.RWMutex
	jobs map[uuid.UUID]*ReportJob
}

func NewStore() *Store {
	return &Store{jobs: make(map[uuid.UUID]*ReportJob)}
}

// Create enqueues a new report job and returns the job record.
func (s *Store) Create(_ context.Context, tenantID uuid.UUID, req ReportRequest) (*ReportJob, error) {
	now := time.Now().UTC()
	format := req.Format
	if format == "" {
		format = "csv"
	}
	job := &ReportJob{
		JobID:      uuid.New(),
		TenantID:   tenantID,
		ReportType: req.ReportType,
		Status:     JobStatusPending,
		Format:     format,
		From:       req.From,
		To:         req.To,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	s.mu.Lock()
	s.jobs[job.JobID] = job
	s.mu.Unlock()
	return job, nil
}

// GetByID returns a job by ID, scoped to the tenant.
func (s *Store) GetByID(_ context.Context, tenantID, jobID uuid.UUID) (*ReportJob, error) {
	s.mu.RLock()
	j, ok := s.jobs[jobID]
	s.mu.RUnlock()
	if !ok || j.TenantID != tenantID {
		return nil, ErrNotFound
	}
	// Return a copy to avoid races on the caller side.
	cp := *j
	return &cp, nil
}

// List returns all jobs for the tenant, newest first.
func (s *Store) List(_ context.Context, tenantID uuid.UUID) ([]*ReportJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*ReportJob
	for _, j := range s.jobs {
		if j.TenantID == tenantID {
			cp := *j
			out = append(out, &cp)
		}
	}
	// Simple newest-first sort on CreatedAt.
	for i := 0; i < len(out); i++ {
		for k := i + 1; k < len(out); k++ {
			if out[k].CreatedAt.After(out[i].CreatedAt) {
				out[i], out[k] = out[k], out[i]
			}
		}
	}
	return out, nil
}
