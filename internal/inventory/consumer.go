// internal/inventory/consumer.go
//
// SaleConsumer drives the background SOH decrement loop.
//
// It polls transaction.transaction_line_items for rows whose
// inventory_movement_id is NULL (not yet processed), joined to
// transaction.transactions for location_id. For each eligible line:
//
//  1. AppendMovement("sale", -qty) or ("return", +qty for is_return rows)
//  2. UPDATE transaction_line_items SET inventory_movement_id = $movID
//  3. If new SOH <= 0, emit a replenishment signal to inventory:replenish
//
// FOR UPDATE SKIP LOCKED on the line ensures two concurrent inventory
// pods never double-process the same line (Valkey pod autoscaling).
//
// Loop 4 SOH event consumer.
package inventory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	replenishStream  = "inventory:replenish"
	consumerBatch    = 100
	consumerInterval = 30 * time.Second
)

// SaleConsumer processes unlinked transaction line items and applies
// corresponding inventory movements.
type SaleConsumer struct {
	pool    *pgxpool.Pool
	store   *Store
	valkey  *redis.Client
	logger  *zap.Logger
	pollInterval time.Duration
}

// NewSaleConsumer constructs a SaleConsumer. pollInterval defaults to 30s.
func NewSaleConsumer(pool *pgxpool.Pool, store *Store, valkey *redis.Client, logger *zap.Logger, pollInterval time.Duration) *SaleConsumer {
	if pollInterval <= 0 {
		pollInterval = consumerInterval
	}
	return &SaleConsumer{
		pool:         pool,
		store:        store,
		valkey:       valkey,
		logger:       logger,
		pollInterval: pollInterval,
	}
}

// Run blocks processing until ctx is cancelled.
func (c *SaleConsumer) Run(ctx context.Context) error {
	c.logger.Info("inventory sale consumer starting", zap.Duration("interval", c.pollInterval))

	// Run once immediately.
	if err := c.tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
		c.logger.Error("consumer tick", zap.Error(err))
	}

	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := c.tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
				c.logger.Error("consumer tick", zap.Error(err))
			}
		}
	}
}

// tick processes one batch of unlinked lines.
func (c *SaleConsumer) tick(ctx context.Context) error {
	lines, err := c.fetchUnlinked(ctx)
	if err != nil {
		return fmt.Errorf("fetch unlinked: %w", err)
	}
	for _, l := range lines {
		if err := c.processLine(ctx, l); err != nil {
			c.logger.Warn("process line failed",
				zap.String("line_id", l.lineID.String()),
				zap.Error(err),
			)
		}
	}
	return nil
}

// unlinkedLine is the minimal projection for the consumer join.
type unlinkedLine struct {
	lineID     uuid.UUID
	tenantID   uuid.UUID
	itemID     uuid.UUID
	locationID uuid.UUID
	quantity   string // numeric text
	isReturn   bool
	costBasis  *string
}

// fetchUnlinked loads up to consumerBatch unlinked, non-void lines with
// a known item_id, locking them with SKIP LOCKED.
func (c *SaleConsumer) fetchUnlinked(ctx context.Context) ([]unlinkedLine, error) {
	const q = `
		SELECT li.id, li.tenant_id, li.item_id,
		       tx.location_id,
		       li.quantity::text, li.is_return, li.cost_basis::text
		  FROM transaction.transaction_line_items li
		  JOIN transaction.transactions tx ON tx.id = li.transaction_id
		 WHERE li.inventory_movement_id IS NULL
		   AND li.is_void = false
		   AND li.item_id IS NOT NULL
		 ORDER BY li.created_at
		 LIMIT $1
		   FOR UPDATE OF li SKIP LOCKED`

	rows, err := c.pool.Query(ctx, q, consumerBatch)
	if err != nil {
		return nil, fmt.Errorf("query unlinked: %w", err)
	}
	defer rows.Close()

	var out []unlinkedLine
	for rows.Next() {
		var l unlinkedLine
		if err := rows.Scan(
			&l.lineID, &l.tenantID, &l.itemID,
			&l.locationID, &l.quantity, &l.isReturn, &l.costBasis,
		); err != nil {
			return nil, fmt.Errorf("scan line: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// processLine applies one inventory movement and links it to the line.
func (c *SaleConsumer) processLine(ctx context.Context, l unlinkedLine) error {
	movType := "sale"
	qty := "-" + l.quantity
	if l.isReturn {
		movType = "return"
		qty = l.quantity
	}

	req := AppendMovementRequest{
		MerchantID:   l.tenantID,
		ItemID:       l.itemID,
		LocationID:   l.locationID,
		MovementType: movType,
		Quantity:     qty,
		CostBasis:    l.costBasis,
	}
	mov, pos, err := c.store.AppendMovement(ctx, req, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("append movement: %w", err)
	}

	if err := c.linkMovement(ctx, l.lineID, mov.ID); err != nil {
		return fmt.Errorf("link movement: %w", err)
	}

	// Emit replenishment signal when SOH reaches or drops below zero.
	if isZeroOrNegative(pos.OnHandQuantity) && !l.isReturn {
		c.emitReplenish(ctx, l.tenantID, l.itemID, l.locationID, pos.OnHandQuantity)
	}
	return nil
}

// linkMovement sets inventory_movement_id on the line item row.
func (c *SaleConsumer) linkMovement(ctx context.Context, lineID, movID uuid.UUID) error {
	_, err := c.pool.Exec(ctx,
		`UPDATE transaction.transaction_line_items
		    SET inventory_movement_id = $1
		  WHERE id = $2`,
		movID, lineID,
	)
	if err != nil {
		return fmt.Errorf("update line: %w", err)
	}
	return nil
}

// emitReplenish publishes a lightweight signal to inventory:replenish.
// Best-effort — failures are logged but never block the SOH path.
func (c *SaleConsumer) emitReplenish(ctx context.Context, tenantID, itemID, locationID uuid.UUID, soh string) {
	args := &redis.XAddArgs{
		Stream: replenishStream,
		Values: map[string]any{
			"tenant_id":   tenantID.String(),
			"item_id":     itemID.String(),
			"location_id": locationID.String(),
			"soh":         soh,
			"emitted_at":  time.Now().UTC().Format(time.RFC3339),
		},
	}
	if _, err := c.valkey.XAdd(ctx, args).Result(); err != nil {
		c.logger.Warn("replenish emit failed",
			zap.String("item_id", itemID.String()),
			zap.String("location_id", locationID.String()),
			zap.Error(err),
		)
	}
}

// isZeroOrNegative returns true when the numeric string represents a
// value ≤ 0. Uses string parsing to avoid pulling in a decimal lib.
func isZeroOrNegative(qty string) bool {
	if qty == "" {
		return true
	}
	// A negative number starts with '-'; zero may be "0", "0.0000", etc.
	if qty[0] == '-' {
		return true
	}
	for _, ch := range qty {
		if ch == '.' {
			continue
		}
		if ch != '0' {
			return false
		}
	}
	return true
}

// linkMovementTx sets inventory_movement_id within a supplied pgx.Tx.
// Used by tests that want to verify the update within a rolled-back tx.
func linkMovementTx(ctx context.Context, tx pgx.Tx, lineID, movID uuid.UUID) error {
	_, err := tx.Exec(ctx,
		`UPDATE transaction.transaction_line_items
		    SET inventory_movement_id = $1
		  WHERE id = $2`,
		movID, lineID,
	)
	return err
}
