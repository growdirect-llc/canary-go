// Package ecom is the channel-aware ecom order ingestion shape.
//
// Adapters live as separate sub-packages or external stubs; this
// package owns the interface contract + a static channel registry
// the portal renders against. Wired W15.
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

// Channel describes a registered ecom channel adapter.
type Channel struct {
	Code        string // e.g. "shopify", "bigcommerce"
	DisplayName string
	Status      string // "available" | "connected" | "disconnected" | "deferred"
	Note        string
}

// The Adapter interface, Order struct, and ChannelHealth struct that
// previously lived here have been removed — they had no callers and
// the contract is better authored from real adapter use rather than
// pre-declared. Re-add when the first real adapter (Shopify) ships
// and the contract is informed by use.

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
