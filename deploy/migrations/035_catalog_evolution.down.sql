-- 035_catalog_evolution.down.sql

-- Drop new tables (FK references handle cleanup of indexes).
DROP TABLE IF EXISTS pricing.observed_price_rules;
DROP TABLE IF EXISTS catalog.import_jobs;
DROP TABLE IF EXISTS catalog.item_serials;

-- Drop additive columns on existing tables.
ALTER TABLE catalog.item_vendors
    DROP COLUMN IF EXISTS order_unit_of_measure,
    DROP COLUMN IF EXISTS vendor_description;

ALTER TABLE catalog.items
    DROP COLUMN IF EXISTS last_received_at,
    DROP COLUMN IF EXISTS status_changed_at,
    DROP COLUMN IF EXISTS preferred_unit_of_measure,
    DROP COLUMN IF EXISTS is_discountable,
    DROP COLUMN IF EXISTS mix_match_code,
    DROP COLUMN IF EXISTS price_decimals,
    DROP COLUMN IF EXISTS qty_decimals,
    DROP COLUMN IF EXISTS tracking_method;

-- Status comment reverts to the comment in 02_catalog_items.sql
-- (active | discontinued | seasonal | hidden) — ALTER COMMENT
-- isn't worth the rollback DDL; comment-only drift is harmless.
