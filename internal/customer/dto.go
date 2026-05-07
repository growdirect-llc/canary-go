// internal/customer/dto.go
//
// Wire types for customer endpoints.
//
//

package customer

import (
	"time"

	"github.com/google/uuid"
)

// CustomerDTO is the wire shape for c.customers.
// PII fields (email, phone, birth_date) are included — callers must
// ensure API keys are scoped for PII access (enforced at platform layer).
type CustomerDTO struct {
	ID                uuid.UUID  `json:"id"`
	TenantID          uuid.UUID  `json:"tenant_id"`
	CustomerCode      *string    `json:"customer_code,omitempty"`
	CustomerType      string     `json:"customer_type"`
	FirstName         *string    `json:"first_name,omitempty"`
	LastName          *string    `json:"last_name,omitempty"`
	DisplayName       *string    `json:"display_name,omitempty"`
	Email             *string    `json:"email,omitempty"`
	Phone             *string    `json:"phone,omitempty"`
	MarketingOptIn    bool       `json:"marketing_opt_in"`
	Status            string     `json:"status"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// LoyaltyMembershipDTO is the wire shape for c.loyalty_memberships.
type LoyaltyMembershipDTO struct {
	ID               uuid.UUID  `json:"id"`
	TenantID         uuid.UUID  `json:"tenant_id"`
	CustomerID       uuid.UUID  `json:"customer_id"`
	ProgramCode      string     `json:"program_code"`
	MembershipNumber string     `json:"membership_number"`
	Tier             *string    `json:"tier,omitempty"`
	PointsBalance    int64      `json:"points_balance"`
	PointsLifetime   int64      `json:"points_lifetime"`
	Status           string     `json:"status"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// ListFilters controls the customer listing.
type ListFilters struct {
	TenantID     uuid.UUID
	Status       string
	CustomerType string
	Search       string // fuzzy match on display_name / email / phone
	Limit        int
	Offset       int
}
