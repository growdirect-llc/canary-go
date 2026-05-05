-- Migration 025: partial index for the inventory sale consumer.
-- The consumer polls transaction_line_items WHERE inventory_movement_id IS NULL
-- AND is_void = false AND item_id IS NOT NULL. A partial index over this
-- exact predicate keeps the scan cheap as the table grows.

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_lines_soh_pending
    ON transaction.transaction_line_items (created_at)
    WHERE inventory_movement_id IS NULL
      AND is_void = false
      AND item_id IS NOT NULL;
