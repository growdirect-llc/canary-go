// Code generated from deploy/schema/01_app_foundation.sql for Loop 2 (GRO-761).
// Wave 1 hand-written types — sqlc retrofit is Loop 3.
// Edit the SQL files in deploy/schema/, regenerate this file by hand.
package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Organization mirrors app.organizations.
type Organization struct {
	ID                uuid.UUID  `db:"id"`
	OrgName           string     `db:"org_name"`
	BillingEmail      *string    `db:"billing_email"`
	SubscriptionTier  string     `db:"subscription_tier"`
	BillingProvider   *string    `db:"billing_provider"`
	BillingExternalID *string    `db:"billing_external_id"`
	BillingStatus     *string    `db:"billing_status"`
	IsActive          bool       `db:"is_active"`
	CreatedAt         time.Time  `db:"created_at"`
	UpdatedAt         time.Time  `db:"updated_at"`
	CreatedBy         *uuid.UUID `db:"created_by"`
	ModifiedBy        *uuid.UUID `db:"modified_by"`
	DBStatus          string     `db:"db_status"`
	DBEffectiveFrom   *time.Time `db:"db_effective_from"`
	DBEffectiveTo     *time.Time `db:"db_effective_to"`
}

// Tenant mirrors app.tenants.
type Tenant struct {
	ID             uuid.UUID       `db:"id"`
	OrganizationID uuid.UUID       `db:"organization_id"`
	TenantCode     string          `db:"tenant_code"`
	Name           string          `db:"name"`
	Status         string          `db:"status"`
	SchemaName     string          `db:"schema_name"`
	Region         string          `db:"region"`
	Attributes     json.RawMessage `db:"attributes"`
	CreatedAt      time.Time       `db:"created_at"`
	UpdatedAt      time.Time       `db:"updated_at"`
}

// Merchant mirrors app.merchants.
type Merchant struct {
	ID               uuid.UUID  `db:"id"`
	OrganizationID   uuid.UUID  `db:"organization_id"`
	TenantID         *uuid.UUID `db:"tenant_id"`
	SourceMerchantID string     `db:"source_merchant_id"`
	MerchantName     string     `db:"merchant_name"`
	Currency         string     `db:"currency"`
	IsActive         bool       `db:"is_active"`
	CreatedAt        time.Time  `db:"created_at"`
	UpdatedAt        time.Time  `db:"updated_at"`
}

// MerchantSettings mirrors app.merchant_settings.
type MerchantSettings struct {
	ID                     uuid.UUID `db:"id"`
	MerchantID             uuid.UUID `db:"merchant_id"`
	Timezone               string    `db:"timezone"`
	Language               string    `db:"language"`
	DateFormat             *string   `db:"date_format"`
	CalendarType           string    `db:"calendar_type"`
	FiscalYearStartMonth   *int16    `db:"fiscal_year_start_month"`
	FiscalWeekStartDay     *int16    `db:"fiscal_week_start_day"`
	FiscalPattern          *string   `db:"fiscal_pattern"`
	NotifEmailEnabled      bool      `db:"notif_email_enabled"`
	NotifSMSEnabled        bool      `db:"notif_sms_enabled"`
	NotifInAppEnabled      bool      `db:"notif_in_app_enabled"`
	NotifQuietHoursStart   *int16    `db:"notif_quiet_hours_start"`
	NotifQuietHoursEnd     *int16    `db:"notif_quiet_hours_end"`
	NotifSeverityThreshold *string   `db:"notif_severity_threshold"`
	NotifDailyLimit        *int32    `db:"notif_daily_limit"`
	NotifPhone             *string   `db:"notif_phone"`
	Theme                  *string   `db:"theme"`
	ShowEmployeeNames      bool      `db:"show_employee_names"`
	CreatedAt              time.Time `db:"created_at"`
	UpdatedAt              time.Time `db:"updated_at"`
}

// Role mirrors app.roles.
type Role struct {
	ID          uuid.UUID `db:"id"`
	RoleName    string    `db:"role_name"`
	Description *string   `db:"description"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

// User mirrors app.users.
type User struct {
	ID              uuid.UUID  `db:"id"`
	MerchantID      uuid.UUID  `db:"merchant_id"`
	Username        string     `db:"username"`
	Email           string     `db:"email"`
	DisplayName     *string    `db:"display_name"`
	IsActive        bool       `db:"is_active"`
	LastLoginAt     *time.Time `db:"last_login_at"`
	CreatedAt       time.Time  `db:"created_at"`
	UpdatedAt       time.Time  `db:"updated_at"`
	CreatedBy       *uuid.UUID `db:"created_by"`
	ModifiedBy      *uuid.UUID `db:"modified_by"`
	DBStatus        string     `db:"db_status"`
	DBEffectiveFrom *time.Time `db:"db_effective_from"`
	DBEffectiveTo   *time.Time `db:"db_effective_to"`
}

// UserRole mirrors app.user_roles.
type UserRole struct {
	ID         uuid.UUID  `db:"id"`
	MerchantID uuid.UUID  `db:"merchant_id"`
	UserID     uuid.UUID  `db:"user_id"`
	RoleID     uuid.UUID  `db:"role_id"`
	CreatedAt  time.Time  `db:"created_at"`
	UpdatedAt  time.Time  `db:"updated_at"`
	CreatedBy  *uuid.UUID `db:"created_by"`
	ModifiedBy *uuid.UUID `db:"modified_by"`
}

// AppLocation mirrors app.locations.
// Prefixed with App to avoid collision with l.locations (Location in l.go).
type AppLocation struct {
	ID               uuid.UUID       `db:"id"`
	MerchantID       uuid.UUID       `db:"merchant_id"`
	SquareLocationID string          `db:"square_location_id"`
	LocationName     string          `db:"location_name"`
	AddressLine1     *string         `db:"address_line1"`
	AddressLine2     *string         `db:"address_line2"`
	City             *string         `db:"city"`
	State            *string         `db:"state"`
	PostalCode       *string         `db:"postal_code"`
	Coordinates      json.RawMessage `db:"coordinates"`
	IsActive         bool            `db:"is_active"`
	CreatedAt        time.Time       `db:"created_at"`
	UpdatedAt        time.Time       `db:"updated_at"`
	CreatedBy        *uuid.UUID      `db:"created_by"`
	ModifiedBy       *uuid.UUID      `db:"modified_by"`
	DBStatus         string          `db:"db_status"`
	DBEffectiveFrom  *time.Time      `db:"db_effective_from"`
	DBEffectiveTo    *time.Time      `db:"db_effective_to"`
}

// AppEmployee mirrors app.employees.
// Prefixed with App to avoid collision with e.employees (Employee in e.go).
type AppEmployee struct {
	ID               uuid.UUID  `db:"id"`
	MerchantID       uuid.UUID  `db:"merchant_id"`
	SquareEmployeeID string     `db:"square_employee_id"`
	EmployeeName     string     `db:"employee_name"`
	Email            *string    `db:"email"`
	RiskScore        string     `db:"risk_score"` // numeric — decimal.Decimal dep needed; using string for Loop 2
	IsActive         bool       `db:"is_active"`
	CreatedAt        time.Time  `db:"created_at"`
	UpdatedAt        time.Time  `db:"updated_at"`
	CreatedBy        *uuid.UUID `db:"created_by"`
	ModifiedBy       *uuid.UUID `db:"modified_by"`
	DBStatus         string     `db:"db_status"`
	DBEffectiveFrom  *time.Time `db:"db_effective_from"`
	DBEffectiveTo    *time.Time `db:"db_effective_to"`
}

// LocationHierarchy mirrors app.location_hierarchy.
// Prefixed differently from l.location_hierarchy via package context (this is in app namespace).
type AppLocationHierarchy struct {
	ID              uuid.UUID  `db:"id"`
	MerchantID      uuid.UUID  `db:"merchant_id"`
	Name            string     `db:"name"`
	Level           int16      `db:"level"`
	ParentID        *uuid.UUID `db:"parent_id"`
	CreatedAt       time.Time  `db:"created_at"`
	UpdatedAt       time.Time  `db:"updated_at"`
	CreatedBy       *uuid.UUID `db:"created_by"`
	ModifiedBy      *uuid.UUID `db:"modified_by"`
	DBStatus        string     `db:"db_status"`
	DBEffectiveFrom *time.Time `db:"db_effective_from"`
	DBEffectiveTo   *time.Time `db:"db_effective_to"`
}

// UserEmployeeLink mirrors app.user_employee_links.
type UserEmployeeLink struct {
	ID         uuid.UUID  `db:"id"`
	MerchantID uuid.UUID  `db:"merchant_id"`
	UserID     uuid.UUID  `db:"user_id"`
	EmployeeID uuid.UUID  `db:"employee_id"`
	CreatedAt  time.Time  `db:"created_at"`
	UpdatedAt  time.Time  `db:"updated_at"`
	CreatedBy  *uuid.UUID `db:"created_by"`
	ModifiedBy *uuid.UUID `db:"modified_by"`
}

// AppEmployeeLocationAssignment mirrors app.employee_location_assignments.
// Prefixed with App to avoid collision with e.employee_location_assignments.
type AppEmployeeLocationAssignment struct {
	ID         uuid.UUID  `db:"id"`
	MerchantID uuid.UUID  `db:"merchant_id"`
	EmployeeID uuid.UUID  `db:"employee_id"`
	LocationID uuid.UUID  `db:"location_id"`
	CreatedAt  time.Time  `db:"created_at"`
	UpdatedAt  time.Time  `db:"updated_at"`
	CreatedBy  *uuid.UUID `db:"created_by"`
	ModifiedBy *uuid.UUID `db:"modified_by"`
}

// SourceSystem mirrors app.source_systems.
type SourceSystem struct {
	Code        string    `db:"code"`
	DisplayName string    `db:"display_name"`
	Category    string    `db:"category"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

// MerchantSource mirrors app.merchant_sources.
type MerchantSource struct {
	ID             uuid.UUID       `db:"id"`
	MerchantID     uuid.UUID       `db:"merchant_id"`
	SourceCode     string          `db:"source_code"`
	RaasNamespace  *string         `db:"raas_namespace"`
	Status         string          `db:"status"`
	MetadataJSON   json.RawMessage `db:"metadata_json"`
	DisconnectedAt *time.Time      `db:"disconnected_at"`
	CreatedAt      time.Time       `db:"created_at"`
	UpdatedAt      time.Time       `db:"updated_at"`
	CreatedBy      *uuid.UUID      `db:"created_by"`
	ModifiedBy     *uuid.UUID      `db:"modified_by"`
}

// ExternalIdentity mirrors app.external_identities.
type ExternalIdentity struct {
	ID         uuid.UUID  `db:"id"`
	MerchantID uuid.UUID  `db:"merchant_id"`
	EntityType string     `db:"entity_type"`
	EntityID   uuid.UUID  `db:"entity_id"`
	SourceCode string     `db:"source_code"`
	ExternalID string     `db:"external_id"`
	IsPrimary  bool       `db:"is_primary"`
	CreatedAt  time.Time  `db:"created_at"`
	UpdatedAt  time.Time  `db:"updated_at"`
	CreatedBy  *uuid.UUID `db:"created_by"`
	ModifiedBy *uuid.UUID `db:"modified_by"`
}

// AuditLog mirrors app.audit_log.
type AuditLog struct {
	ID            uuid.UUID  `db:"id"`
	MerchantID    *uuid.UUID `db:"merchant_id"`
	UserID        *uuid.UUID `db:"user_id"`
	Action        string     `db:"action"`
	Resource      *string    `db:"resource"`
	ResourceID    *uuid.UUID `db:"resource_id"`
	IPAddress     *string    `db:"ip_address"`
	CreatedAt     time.Time  `db:"created_at"`
	EventID       *uuid.UUID `db:"event_id"`
	PayloadDigest *string    `db:"payload_digest"`
	SourceCode    *string    `db:"source_code"`
	RequestID     *string    `db:"request_id"`
	UserAgent     *string    `db:"user_agent"`
	StatusCode    *int32     `db:"status_code"`
	LatencyMs     *int32     `db:"latency_ms"`
	ActorType     *string    `db:"actor_type"`
	MCPServer     *string    `db:"mcp_server"`
	ToolName      *string    `db:"tool_name"`
}

// InterestSignup mirrors app.interest_signups.
type InterestSignup struct {
	ID        uuid.UUID `db:"id"`
	Email     string    `db:"email"`
	CreatedAt time.Time `db:"created_at"`
}

// HawkOAuthToken mirrors app.hawk_oauth_tokens.
type HawkOAuthToken struct {
	ID                    uuid.UUID  `db:"id"`
	MerchantID            uuid.UUID  `db:"merchant_id"`
	AccessTokenEncrypted  string     `db:"access_token_encrypted"`
	RefreshTokenEncrypted *string    `db:"refresh_token_encrypted"`
	TokenType             string     `db:"token_type"`
	ExpiresAt             time.Time  `db:"expires_at"`
	Scopes                *string    `db:"scopes"`
	CreatedAt             time.Time  `db:"created_at"`
	UpdatedAt             time.Time  `db:"updated_at"`
	CreatedBy             *uuid.UUID `db:"created_by"`
	ModifiedBy            *uuid.UUID `db:"modified_by"`
}

// BullAPICredential mirrors app.bull_api_credentials.
type BullAPICredential struct {
	ID              uuid.UUID  `db:"id"`
	MerchantID      uuid.UUID  `db:"merchant_id"`
	APIKeyEncrypted string     `db:"api_key_encrypted"`
	EndpointURL     string     `db:"endpoint_url"`
	IsActive        bool       `db:"is_active"`
	CreatedAt       time.Time  `db:"created_at"`
	UpdatedAt       time.Time  `db:"updated_at"`
	CreatedBy       *uuid.UUID `db:"created_by"`
	ModifiedBy      *uuid.UUID `db:"modified_by"`
}

// BullPollWatermark mirrors app.bull_poll_watermarks.
type BullPollWatermark struct {
	ID           uuid.UUID  `db:"id"`
	MerchantID   uuid.UUID  `db:"merchant_id"`
	EndpointName string     `db:"endpoint_name"`
	LastModified time.Time  `db:"last_modified"`
	LastRunAt    *time.Time `db:"last_run_at"`
	CreatedAt    time.Time  `db:"created_at"`
	UpdatedAt    time.Time  `db:"updated_at"`
}

// BullMerchantConfig mirrors app.bull_merchant_config.
type BullMerchantConfig struct {
	ID            uuid.UUID `db:"id"`
	MerchantID    uuid.UUID `db:"merchant_id"`
	PollIntervalS int32     `db:"poll_interval_s"`
	CreatedAt     time.Time `db:"created_at"`
	UpdatedAt     time.Time `db:"updated_at"`
}

// BullEventLog mirrors app.bull_event_log.
type BullEventLog struct {
	ID          uuid.UUID       `db:"id"`
	MerchantID  uuid.UUID       `db:"merchant_id"`
	EventType   string          `db:"event_type"`
	Payload     json.RawMessage `db:"payload"`
	ProcessedAt *time.Time      `db:"processed_at"`
	CreatedAt   time.Time       `db:"created_at"`
}
