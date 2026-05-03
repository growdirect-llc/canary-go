// internal/report/storer.go
package report

import (
	"context"

	"github.com/google/uuid"
)

// Storer is the report job access contract. Both the in-memory Store
// (tests) and PgxStore (production) implement this interface.
type Storer interface {
	Create(ctx context.Context, tenantID uuid.UUID, req ReportRequest) (*ReportJob, error)
	GetByID(ctx context.Context, tenantID, jobID uuid.UUID) (*ReportJob, error)
	List(ctx context.Context, tenantID uuid.UUID) ([]*ReportJob, error)
}
