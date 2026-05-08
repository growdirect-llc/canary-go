// internal/mcp/asset.go
package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/ruptiv/canary/internal/asset"
	"github.com/ruptiv/canary/internal/identity"
)

// RegisterAssetTools registers 4 asset tools with the registry.
func RegisterAssetTools(reg *Registry, s *asset.Store) {
	reg.Register(ToolDef{
		Name:        "canary.asset.list",
		Description: "List inventory positions. Filters: location_id, status (active|discontinued), low_stock (bool), limit, offset.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"location_id":{"type":"string","format":"uuid"},"status":{"type":"string","enum":["active","discontinued"]},"low_stock":{"type":"boolean"},"limit":{"type":"integer"},"offset":{"type":"integer"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		claims, ok := identity.ClaimsFromContext(ctx)
		if !ok {
			return nil, errUnauth
		}
		var p struct {
			LocationID *string `json:"location_id"`
			Status     string  `json:"status"`
			LowStock   bool    `json:"low_stock"`
			Limit      int     `json:"limit"`
			Offset     int     `json:"offset"`
		}
		_ = json.Unmarshal(args, &p)
		f := asset.ListFilters{TenantID: claims.TenantID, Status: p.Status, LowStock: p.LowStock, Limit: p.Limit, Offset: p.Offset}
		if p.LocationID != nil {
			if id, err := uuid.Parse(*p.LocationID); err == nil {
				f.LocationID = &id
			}
		}
		return s.List(ctx, f)
	})

	reg.Register(ToolDef{
		Name:        "canary.asset.get_item",
		Description: "Get an item master record with all inventory positions and lots across locations.",
		InputSchema: json.RawMessage(`{"type":"object","required":["item_id"],"properties":{"item_id":{"type":"string","format":"uuid"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		claims, ok := identity.ClaimsFromContext(ctx)
		if !ok {
			return nil, errUnauth
		}
		var p struct{ ItemID string `json:"item_id"` }
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, err
		}
		id, err := uuid.Parse(p.ItemID)
		if err != nil {
			return nil, err
		}
		return s.GetItem(ctx, claims.TenantID, id)
	})

	reg.Register(ToolDef{
		Name:        "canary.asset.shrink_movements",
		Description: "Aggregate write-off and negative-adjustment inventory movements by type and reason code over a date range.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"from":{"type":"string","format":"date"},"to":{"type":"string","format":"date"},"location_id":{"type":"string","format":"uuid"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		claims, ok := identity.ClaimsFromContext(ctx)
		if !ok {
			return nil, errUnauth
		}
		var p struct {
			From       string  `json:"from"`
			To         string  `json:"to"`
			LocationID *string `json:"location_id"`
		}
		_ = json.Unmarshal(args, &p)
		now := time.Now().UTC()
		from, err := time.ParseInLocation(dateLayout, p.From, time.UTC)
		if err != nil {
			from = now.AddDate(0, 0, -30)
		}
		to, err2 := time.ParseInLocation(dateLayout, p.To, time.UTC)
		if err2 != nil {
			to = now
		}
		var locID *uuid.UUID
		if p.LocationID != nil {
			if id, err3 := uuid.Parse(*p.LocationID); err3 == nil {
				locID = &id
			}
		}
		return s.ShrinkMovements(ctx, claims.TenantID, from, to, locID)
	})

	reg.Register(ToolDef{
		Name:        "canary.asset.flag",
		Description: "Create an inventory discrepancy flag (writes an adjustment movement row). Does not update SOH directly — Bull pipeline reconciles. quantity_delta is negative for a loss.",
		InputSchema: json.RawMessage(`{"type":"object","required":["item_id","location_id","quantity_delta","reason_code"],"properties":{"item_id":{"type":"string","format":"uuid"},"location_id":{"type":"string","format":"uuid"},"quantity_delta":{"type":"number"},"reason_code":{"type":"string","enum":["theft","damaged","spoilage","recount_corrected"]},"reference":{"type":"string"},"performed_by_user_id":{"type":"string","format":"uuid"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		claims, ok := identity.ClaimsFromContext(ctx)
		if !ok {
			return nil, errUnauth
		}
		var p struct {
			ItemID            string  `json:"item_id"`
			LocationID        string  `json:"location_id"`
			QuantityDelta     float64 `json:"quantity_delta"`
			ReasonCode        string  `json:"reason_code"`
			Reference         string  `json:"reference"`
			PerformedByUserID *string `json:"performed_by_user_id"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, err
		}
		itemID, err := uuid.Parse(p.ItemID)
		if err != nil {
			return nil, err
		}
		locID, err := uuid.Parse(p.LocationID)
		if err != nil {
			return nil, err
		}
		req := asset.FlagRequest{LocationID: locID, QuantityDelta: p.QuantityDelta, ReasonCode: p.ReasonCode, Reference: p.Reference}
		if p.PerformedByUserID != nil {
			if id, err2 := uuid.Parse(*p.PerformedByUserID); err2 == nil {
				req.PerformedByUser = &id
			}
		}
		return s.Flag(ctx, claims.TenantID, itemID, req)
	})
}
