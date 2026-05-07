// Package task implements the directed-task queue for mobile store operators.
// Supports three task types (receiving, replenishment, cycle_count) and a
// seven-state status machine (queued → assigned → in_progress → complete →
// verified; side exits: skipped, cancelled).
//
// Loop 4 mobile task queue.
package task

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// TaskType enumerates the three mobile task types.
const (
	TypeReceiving     = "receiving"
	TypeReplenishment = "replenishment"
	TypeCycleCount    = "cycle_count"
)

var validTypes = map[string]struct{}{
	TypeReceiving:     {},
	TypeReplenishment: {},
	TypeCycleCount:    {},
}

// TaskStatus models the directed-work lifecycle.
const (
	StatusQueued     = "queued"
	StatusAssigned   = "assigned"
	StatusInProgress = "in_progress"
	StatusComplete   = "complete"
	StatusVerified   = "verified"
	StatusSkipped    = "skipped"
	StatusCancelled  = "cancelled"
)

// ExceptionCode enumerates valid exception reason codes.
var validExceptionCodes = map[string]struct{}{
	"damage":          {},
	"wrong_qty":       {},
	"location_blocked": {},
	"item_not_found":  {},
	"other":           {},
}

// Sentinel errors.
var (
	ErrNotFound           = errors.New("task: not found")
	ErrInvalidType        = errors.New("task: invalid task_type")
	ErrInvalidTransition  = errors.New("task: invalid status transition")
	ErrInvalidExceptionCode = errors.New("task: invalid exception reason_code")
	ErrMissingField       = errors.New("task: missing required field")
)

// TaskDTO is the wire shape for a directed_tasks row.
type TaskDTO struct {
	ID               uuid.UUID       `json:"id"`
	TenantID         uuid.UUID       `json:"tenant_id"`
	TaskType         string          `json:"task_type"`
	Priority         int             `json:"priority"`
	Status           string          `json:"status"`
	ItemID           *uuid.UUID      `json:"item_id,omitempty"`
	LocationID       *uuid.UUID      `json:"location_id,omitempty"`
	ZoneID           *uuid.UUID      `json:"zone_id,omitempty"`
	Quantity         *string         `json:"quantity,omitempty"`
	SourceLocationID *uuid.UUID      `json:"source_location_id,omitempty"`
	AssigneeID       *uuid.UUID      `json:"assignee_id,omitempty"`
	AssignedAt       *time.Time      `json:"assigned_at,omitempty"`
	StartedAt        *time.Time      `json:"started_at,omitempty"`
	CompletedAt      *time.Time      `json:"completed_at,omitempty"`
	VerifiedAt       *time.Time      `json:"verified_at,omitempty"`
	EstimatedSeconds *int            `json:"estimated_seconds,omitempty"`
	SkipReason       *string         `json:"skip_reason,omitempty"`
	SourceRef        *string         `json:"source_ref,omitempty"`
	Attributes       json.RawMessage `json:"attributes,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// ExceptionDTO is the wire shape for a task_exceptions row.
type ExceptionDTO struct {
	ID         uuid.UUID  `json:"id"`
	TaskID     uuid.UUID  `json:"task_id"`
	TenantID   uuid.UUID  `json:"tenant_id"`
	ReasonCode string     `json:"reason_code"`
	Note       *string    `json:"note,omitempty"`
	ReportedBy *uuid.UUID `json:"reported_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// CreateTaskRequest is the POST /tasks body.
type CreateTaskRequest struct {
	TenantID         uuid.UUID       `json:"tenant_id"`
	TaskType         string          `json:"task_type"`
	Priority         int             `json:"priority,omitempty"`
	ItemID           *uuid.UUID      `json:"item_id,omitempty"`
	LocationID       *uuid.UUID      `json:"location_id,omitempty"`
	ZoneID           *uuid.UUID      `json:"zone_id,omitempty"`
	Quantity         *string         `json:"quantity,omitempty"`
	SourceLocationID *uuid.UUID      `json:"source_location_id,omitempty"`
	EstimatedSeconds *int            `json:"estimated_seconds,omitempty"`
	SourceRef        *string         `json:"source_ref,omitempty"`
	Attributes       json.RawMessage `json:"attributes,omitempty"`
}

// UpdateStatusRequest is the PATCH /tasks/:id/status body.
type UpdateStatusRequest struct {
	Status string `json:"status"`
}

// ExceptionRequest is the POST /tasks/:id/exception body.
type ExceptionRequest struct {
	ReasonCode string     `json:"reason_code"`
	Note       *string    `json:"note,omitempty"`
	ReportedBy *uuid.UUID `json:"reported_by,omitempty"`
}

// SkipRequest is the POST /tasks/:id/skip body.
type SkipRequest struct {
	Reason string `json:"reason"`
}

// ValidateCreate validates a CreateTaskRequest, returning a normalised copy.
func ValidateCreate(req CreateTaskRequest) (CreateTaskRequest, error) {
	if req.TenantID == uuid.Nil {
		return req, errors.New("task: missing tenant_id")
	}
	if _, ok := validTypes[req.TaskType]; !ok {
		return req, ErrInvalidType
	}
	if req.Priority == 0 {
		req.Priority = 3 // default mid-priority
	}
	if req.Priority < 1 || req.Priority > 5 {
		return req, errors.New("task: priority must be 1–5")
	}
	return req, nil
}

// ValidateTransition checks whether moving from `from` to `to` is legal.
func ValidateTransition(from, to string) error {
	allowed := map[string]map[string]bool{
		StatusQueued:     {StatusAssigned: true, StatusSkipped: true, StatusCancelled: true},
		StatusAssigned:   {StatusInProgress: true, StatusQueued: true, StatusSkipped: true, StatusCancelled: true},
		StatusInProgress: {StatusComplete: true, StatusSkipped: true, StatusCancelled: true},
		StatusComplete:   {StatusVerified: true, StatusCancelled: true},
		StatusVerified:   {},
		StatusSkipped:    {},
		StatusCancelled:  {},
	}
	if moves, ok := allowed[from]; ok && moves[to] {
		return nil
	}
	return ErrInvalidTransition
}
