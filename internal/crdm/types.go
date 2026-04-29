// internal/crdm/types.go
// Canonical Retail Data Model — ARTS POSLOG-aligned Go types.
// All services use these types for cross-service data contracts.
// Do not define competing structs in service packages.
package crdm

import (
	"time"

	"github.com/google/uuid"
)

type Merchant struct {
	ID               uuid.UUID
	OrganizationID   uuid.UUID
	SourceMerchantID string
	MerchantName     string
	Currency         string
	IsActive         bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Location struct {
	ID               uuid.UUID
	MerchantID       uuid.UUID
	SourceLocationID string
	LocationName     string
	City             string
	State            string
	IsActive         bool
}

type Employee struct {
	ID               uuid.UUID
	MerchantID       uuid.UUID
	SourceEmployeeID string
	EmployeeName     string
	RiskScore        float64
	IsActive         bool
}

type TransactionHeader struct {
	ID                uuid.UUID
	MerchantID        uuid.UUID
	ExternalID        string
	ARTSBusinessDate  time.Time
	ARTSWorkstationID string
	TransactionType   string
	TotalCents        int64
	SubtotalCents     int64
	TaxCents          int64
	TipCents          int64
	DiscountCents     int64
	TenderType        string
	SourceCode        string
}

type User struct {
	ID          uuid.UUID
	MerchantID  uuid.UUID
	Username    string
	Email       string
	DisplayName string
	IsActive    bool
	Roles       []string
}
