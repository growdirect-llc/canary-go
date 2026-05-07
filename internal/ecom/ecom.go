// Package ecom is the channel-aware ecom order ingestion shape.
//
// Adapters live as separate sub-packages or external stubs; this
// package owns the interface contract + a static channel registry
// the portal renders against. Wired W15 / GRO-830.
//
// Inventory unification: ecom orders SHOULD consume from the same
// SOH as in-store. The in-store consume path lives at
// `inventory.Store.AppendMovement`. An ecom adapter that processes
// an order calls AppendMovement with `movement_type = 'sale'` and
// `source_system = '<channel_code>'` so the SOH consumer doesn't
// double-count.
//
// Out of scope per dispatch: multi-channel pricing rules, ecom site
// builder, marketplace integration (Amazon/eBay).

package ecom

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Channel describes a registered ecom channel adapter.
type Channel struct {
	Code        string // e.g. "shopify", "bigcommerce"
	DisplayName string
	Status      string // "available" | "connected" | "disconnected" | "deferred"
	Note        string
}

// Order is the wire-shape for one channel-delivered order.
type Order struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	ChannelCode   string
	ChannelOrder  string // channel-side order id
	OrderedAt     time.Time
	Status        string
	TotalCost     string
	LineCount     int
}

// Adapter is the interface every channel adapter implements. Mirrors
// the shape of `internal/squareauth` for the OAuth surfaces.
//
// Adapter implementations do NOT live in this package — they ship as
// `internal/ecom/shopify`, `internal/ecom/bigcommerce`, etc.
type Adapter interface {
	// Code returns the channel discriminator stored on Order rows.
	Code() string
	// Connect runs the OAuth / API-key handshake for a tenant.
	Connect(ctx context.Context, tenantID uuid.UUID, opts map[string]string) error
	// SyncOrders pulls orders since a watermark and returns them.
	SyncOrders(ctx context.Context, tenantID uuid.UUID, since time.Time) ([]Order, error)
	// Health returns connection status + last-sync timestamp.
	Health(ctx context.Context, tenantID uuid.UUID) (ChannelHealth, error)
}

// ChannelHealth is what /ecom/sync renders per channel.
type ChannelHealth struct {
	Code         string
	Connected    bool
	LastSyncAt   *time.Time
	LastError    string
	OrdersToday  int
}

// Registry returns the static channel inventory the portal renders.
// Real adapters get added here as they ship; today this is the
// dispatch's documented surface.
func Registry() []Channel {
	return []Channel{
		{Code: "shopify", DisplayName: "Shopify", Status: "deferred", Note: "Adapter contract defined; SDK wiring deferred per dispatch. First adapter to ship when a Shopify-flagged tenant lands."},
		{Code: "bigcommerce", DisplayName: "BigCommerce", Status: "deferred", Note: "Future adapter slot."},
		{Code: "woocommerce", DisplayName: "WooCommerce", Status: "deferred", Note: "Future adapter slot."},
	}
}
