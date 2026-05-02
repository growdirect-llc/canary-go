// Code generated from deploy/schema/10_ledger.sql for Loop 2 (GRO-761).
// Wave 1 hand-written types — sqlc retrofit is Loop 3.
// Edit the SQL files in deploy/schema/, regenerate this file by hand.
package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// StockLedgerEntry mirrors ledger.stock_ledger_entries. No UpdatedAt (financial append).
type StockLedgerEntry struct {
	ID                  uuid.UUID       `db:"id"`
	TenantID            uuid.UUID       `db:"tenant_id"`
	InventoryMovementID uuid.UUID       `db:"inventory_movement_id"`
	PostedAt            time.Time       `db:"posted_at"`
	ItemID              uuid.UUID       `db:"item_id"`
	LocationID          uuid.UUID       `db:"location_id"`
	QuantityDelta       string          `db:"quantity_delta"` // numeric — decimal.Decimal dep needed; using string for Loop 2
	CostPerUnit         string          `db:"cost_per_unit"`  // numeric — decimal.Decimal dep needed; using string for Loop 2
	CostAmount          string          `db:"cost_amount"`    // numeric — decimal.Decimal dep needed; using string for Loop 2 (GENERATED)
	CostMethod          string          `db:"cost_method"`
	GLAccountID         *uuid.UUID      `db:"gl_account_id"`
	Attributes          json.RawMessage `db:"attributes"`
	CreatedAt           time.Time       `db:"created_at"`
}

// ILDWACPosition mirrors ledger.ildwac_positions. No UpdatedAt (append-only).
type ILDWACPosition struct {
	ID                   uuid.UUID       `db:"id"`
	TenantID             uuid.UUID       `db:"tenant_id"`
	PositionPeriod       string          `db:"position_period"` // tstzrange — string with TODO: tstzrange-aware type for Loop 3
	CadenceStep          string          `db:"cadence_step"`
	LStorageSatoshis     int64           `db:"l_storage_satoshis"`
	WWorkloadSatoshis    int64           `db:"w_workload_satoshis"`
	CCaptureSatoshis     int64           `db:"c_capture_satoshis"`
	TotalSatoshis        int64           `db:"total_satoshis"` // GENERATED
	BytesUnderManagement *int64          `db:"bytes_under_management"`
	WorkloadUnits        *int64          `db:"workload_units"`
	CaptureTier          *string         `db:"capture_tier"`
	InvoicedAt           *time.Time      `db:"invoiced_at"`
	PaymentProof         *string         `db:"payment_proof"`
	Attributes           json.RawMessage `db:"attributes"`
	CreatedAt            time.Time       `db:"created_at"`
}

// RIBBatch mirrors ledger.rib_batches.
type RIBBatch struct {
	ID               uuid.UUID       `db:"id"`
	TenantID         uuid.UUID       `db:"tenant_id"`
	ItemID           uuid.UUID       `db:"item_id"`
	LocationID       *uuid.UUID      `db:"location_id"`
	BatchPeriod      string          `db:"batch_period"`        // tstzrange — string with TODO: tstzrange-aware type for Loop 3
	TotalQuantity    string          `db:"total_quantity"`      // numeric — decimal.Decimal dep needed; using string for Loop 2
	TotalCost        string          `db:"total_cost"`          // numeric — decimal.Decimal dep needed; using string for Loop 2
	WeightedAvgCost  string          `db:"weighted_avg_cost"`   // numeric — decimal.Decimal dep needed; using string for Loop 2 (GENERATED)
	ReceiptCount     int32           `db:"receipt_count"`
	ClosedAt         *time.Time      `db:"closed_at"`
	Attributes       json.RawMessage `db:"attributes"`
	CreatedAt        time.Time       `db:"created_at"`
	UpdatedAt        time.Time       `db:"updated_at"`
}

// L402OTBBudget mirrors ledger.l402_otb_budgets.
type L402OTBBudget struct {
	ID                uuid.UUID       `db:"id"`
	TenantID          uuid.UUID       `db:"tenant_id"`
	BudgetPeriod      string          `db:"budget_period"` // tstzrange — string with TODO: tstzrange-aware type for Loop 3
	ScopeType         string          `db:"scope_type"`
	ScopeID           *uuid.UUID      `db:"scope_id"`
	BudgetSatoshis    int64           `db:"budget_satoshis"`
	ConsumedSatoshis  int64           `db:"consumed_satoshis"`
	RemainingSatoshis int64           `db:"remaining_satoshis"` // GENERATED
	HardLimit         bool            `db:"hard_limit"`
	AlertThresholdPct *string         `db:"alert_threshold_pct"` // numeric — decimal.Decimal dep needed; using string for Loop 2
	Status            string          `db:"status"`
	Attributes        json.RawMessage `db:"attributes"`
	CreatedAt         time.Time       `db:"created_at"`
	UpdatedAt         time.Time       `db:"updated_at"`
}

// BlockchainAnchor mirrors ledger.blockchain_anchors. No UpdatedAt.
type BlockchainAnchor struct {
	ID                 uuid.UUID       `db:"id"`
	TenantID           *uuid.UUID      `db:"tenant_id"`
	AnchorType         string          `db:"anchor_type"`
	PayloadHash        string          `db:"payload_hash"`
	MerkleRoot         *string         `db:"merkle_root"`
	AnchoredAt         time.Time       `db:"anchored_at"`
	L2Chain            string          `db:"l2_chain"`
	L2TransactionID    *string         `db:"l2_transaction_id"`
	L2BlockHeight      *int64          `db:"l2_block_height"`
	L2Proof            json.RawMessage `db:"l2_proof"`
	RelatedEntityCount *int32          `db:"related_entity_count"`
	Status             string          `db:"status"`
	Attributes         json.RawMessage `db:"attributes"`
	CreatedAt          time.Time       `db:"created_at"`
}
