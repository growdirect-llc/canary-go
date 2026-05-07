// internal/report/dto.go
//
// Wire types for the report generation endpoints.
//
// The report service is a thin job-dispatch layer. Clients POST a report
// request, receive a job ID, and poll GET /v1/reports/{job_id} for status
// and a download URL. Actual generation is handled by background workers
// (Wave E+); this module ships the API contract.
//
//

package report

import (
	"time"

	"github.com/google/uuid"
)

// ReportType enumerates the supported report types.
type ReportType string

const (
	ReportTypeSalesSummary ReportType = "sales_summary"
	ReportTypeReturnDetail ReportType = "return_detail"
	ReportTypeShrink       ReportType = "shrink"
)

// JobStatus is the lifecycle state of a report job.
type JobStatus string

const (
	JobStatusPending    JobStatus = "pending"
	JobStatusProcessing JobStatus = "processing"
	JobStatusDone       JobStatus = "done"
	JobStatusFailed     JobStatus = "failed"
)

// ReportRequest is the body for POST /v1/reports.
type ReportRequest struct {
	ReportType ReportType `json:"report_type"`
	From       string     `json:"from"` // YYYY-MM-DD
	To         string     `json:"to"`
	LocationID *uuid.UUID `json:"location_id,omitempty"`
	Format     string     `json:"format"` // csv | xlsx | json — default: csv
}

// ReportJob is the job record returned to the client.
type ReportJob struct {
	JobID      uuid.UUID  `json:"job_id"`
	TenantID   uuid.UUID  `json:"tenant_id"`
	ReportType ReportType `json:"report_type"`
	Status     JobStatus  `json:"status"`
	Format     string     `json:"format"`
	From       string     `json:"from"`
	To         string     `json:"to"`
	DownloadURL *string   `json:"download_url,omitempty"`
	Error       *string   `json:"error,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}
