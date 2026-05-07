// Code generated from deploy/schema/04_c_e_customers_employees.sql for Loop 2.
// Wave 1 hand-written types — sqlc retrofit is Loop 3.
// Edit the SQL files in deploy/schema/, regenerate this file by hand.
package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Customer mirrors c.customers.
type Customer struct {
	ID                uuid.UUID       `db:"id"`
	TenantID          uuid.UUID       `db:"tenant_id"`
	CustomerCode      *string         `db:"customer_code"`
	CustomerType      string          `db:"customer_type"`
	FirstName         *string         `db:"first_name"`
	LastName          *string         `db:"last_name"`
	DisplayName       *string         `db:"display_name"`
	Email             *string         `db:"email"`
	Phone             *string         `db:"phone"`
	BirthDate         *time.Time      `db:"birth_date"`
	PreferredLanguage *string         `db:"preferred_language"`
	MarketingOptIn    bool            `db:"marketing_opt_in"`
	PrimaryAddress    json.RawMessage `db:"primary_address"`
	Attributes        json.RawMessage `db:"attributes"`
	Status            string          `db:"status"`
	MergedInto        *uuid.UUID      `db:"merged_into"`
	ExternalIDs       json.RawMessage `db:"external_ids"`
	CreatedAt         time.Time       `db:"created_at"`
	UpdatedAt         time.Time       `db:"updated_at"`
}

// CustomerAddress mirrors c.customer_addresses.
type CustomerAddress struct {
	ID            uuid.UUID       `db:"id"`
	TenantID      uuid.UUID       `db:"tenant_id"`
	CustomerID    uuid.UUID       `db:"customer_id"`
	AddressType   string          `db:"address_type"`
	RecipientName *string         `db:"recipient_name"`
	Line1         string          `db:"line_1"`
	Line2         *string         `db:"line_2"`
	City          string          `db:"city"`
	Region        *string         `db:"region"`
	PostalCode    *string         `db:"postal_code"`
	Country       string          `db:"country"`
	Latitude      *string         `db:"latitude"`  // numeric — decimal.Decimal dep needed; using string for Loop 2
	Longitude     *string         `db:"longitude"` // numeric — decimal.Decimal dep needed; using string for Loop 2
	IsDefault     bool            `db:"is_default"`
	Attributes    json.RawMessage `db:"attributes"`
	Status        string          `db:"status"`
	CreatedAt     time.Time       `db:"created_at"`
	UpdatedAt     time.Time       `db:"updated_at"`
}

// LoyaltyMembership mirrors c.loyalty_memberships.
type LoyaltyMembership struct {
	ID               uuid.UUID       `db:"id"`
	TenantID         uuid.UUID       `db:"tenant_id"`
	CustomerID       uuid.UUID       `db:"customer_id"`
	ProgramCode      string          `db:"program_code"`
	MembershipNumber string          `db:"membership_number"`
	EnrollmentDate   time.Time       `db:"enrollment_date"`
	Tier             *string         `db:"tier"`
	PointsBalance    int64           `db:"points_balance"`
	PointsLifetime   int64           `db:"points_lifetime"`
	BirthDate        *time.Time      `db:"birth_date"`
	Preferences      json.RawMessage `db:"preferences"`
	Attributes       json.RawMessage `db:"attributes"`
	Status           string          `db:"status"`
	ExpiresAt        *time.Time      `db:"expires_at"`
	CreatedAt        time.Time       `db:"created_at"`
	UpdatedAt        time.Time       `db:"updated_at"`
}
