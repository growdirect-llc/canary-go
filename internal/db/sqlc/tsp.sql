-- internal/db/sqlc/tsp.sql

-- name: InsertIngestionLog :one
INSERT INTO app.ingestion_log (merchant_id, event_id, source_code, chain_hash, stage)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, merchant_id, event_id, source_code, chain_hash, received_at, stage;

-- name: GetIngestionLogByEventID :one
SELECT id, merchant_id, event_id, source_code, chain_hash, received_at, processed_at, stage
FROM app.ingestion_log
WHERE event_id = $1;

-- name: InsertTransaction :one
INSERT INTO sales.transactions (
    merchant_id, external_id, location_id, employee_id,
    arts_business_date, transaction_type, total_cents,
    subtotal_cents, tax_cents, tender_type, source_code
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING id, merchant_id, external_id, arts_business_date, total_cents, created_at;
