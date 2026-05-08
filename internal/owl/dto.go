// Package owl is the merchant intelligence aggregator. Read-only over
// canonical schema (t.*, q.*, m.*, l.*, e.*, app.*) — no writes.
//
// The owl.md SDD describes a much
// broader AI / MCP server (chat, personalities, embeddings, LLM
// inference). The overrides that scope: this build is
// a read-only dashboard aggregator that turns canonical sales + cases
// + detections data into the metrics the merchant operator looks at.
// The chat/MCP layer is left for a later loop.
//
// Owl is the canary for whether the canonical schema can answer the
// questions a merchant operator actually asks. Each `// SDD-missing:`
// or `// SDD-conflict:` comment in this package or its sub-packages
// is a query that revealed a schema gap.
//
// File layout:
//
//	internal/owl/dtotypes/ — leaf DTOs (Period, SalesSummary, ...)
//	internal/owl/metrics/ — raw-SQL query helpers per metric family
//	internal/owl/period.go — period parsing
//	internal/owl/store.go — pgx Store interface + impl (resolve only)
//	internal/owl/aggregator.go — top-level Aggregate(ctx, mid, period)
//	internal/owl/handler.go — chi handlers + Mount
//	cmd/owl/main.go — service entry point
package owl

import "github.com/ruptiv/canary/internal/owl/dtotypes"

// Type aliases re-export dtotypes so callers see a clean owl.* surface.
// dtotypes is the leaf package that both owl/ and owl/metrics/ import
// to break what would otherwise be an import cycle.
type (
	PeriodKind      = dtotypes.PeriodKind
	Period          = dtotypes.Period
	SalesSummary    = dtotypes.SalesSummary
	TopItemsBy      = dtotypes.TopItemsBy
	ItemMetric      = dtotypes.ItemMetric
	TopItems        = dtotypes.TopItems
	LocationMetric  = dtotypes.LocationMetric
	CasesSummary    = dtotypes.CasesSummary
	DetectionRate   = dtotypes.DetectionRate
	CashierExposure = dtotypes.CashierExposure
	Dashboard       = dtotypes.Dashboard
)

// PeriodKind constants are re-exported as well.
const (
	PeriodToday  = dtotypes.PeriodToday
	PeriodWTD    = dtotypes.PeriodWTD
	PeriodMTD    = dtotypes.PeriodMTD
	PeriodCustom = dtotypes.PeriodCustom

	TopItemsByUnits   = dtotypes.TopItemsByUnits
	TopItemsByRevenue = dtotypes.TopItemsByRevenue
)
