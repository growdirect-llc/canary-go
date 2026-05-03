-- internal/db/sqlc/transaction.sql
--
-- Named queries for the t.transactions canonical write path. The store
-- in internal/transaction/store.go orchestrates these within a pgx.Tx
-- for the multi-statement Create/Void/Return operations.
--
-- GENERATED columns (line_total, extended_price, extended_tax, margin)
-- are excluded from INSERT statements — Postgres computes them. They
-- appear in RETURNING / SELECT clauses.
--
-- Spec: GRO-764 Phase B.1 + GRO-765 Phase D carry-forward B.3.

-- name: InsertTransactionHeader :one
INSERT INTO t.transactions (
    tenant_id, transaction_number, transaction_type, parent_transaction_id,
    location_id, pos_terminal_id, cashier_employee_id, customer_id,
    loyalty_membership_id, business_date, started_at, ended_at, status,
    ticket_number, item_count, subtotal, tax_total, discount_total,
    grand_total, currency, channel, pos_software_version,
    is_training_mode, is_offline, attributes, external_ids
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10::date, $11, $12, $13,
    $14, $15, $16::numeric, $17::numeric, $18::numeric, $19::numeric,
    $20, $21, $22, $23, $24,
    COALESCE($25::jsonb, '{}'::jsonb),
    COALESCE($26::jsonb, '{}'::jsonb)
)
RETURNING id, tenant_id, transaction_number, transaction_type,
          parent_transaction_id, location_id, pos_terminal_id,
          cashier_employee_id, customer_id, loyalty_membership_id,
          party_id, business_date::text, started_at, ended_at,
          status, ticket_number, item_count,
          subtotal::numeric, tax_total::numeric,
          discount_total::numeric, grand_total::numeric,
          currency, channel, is_training_mode, is_offline,
          void_reason, created_at, updated_at;

-- name: InsertLineItem :exec
INSERT INTO t.transaction_line_items (
    tenant_id, transaction_id, line_number, item_id, description,
    quantity, unit_of_measure, unit_price, unit_tax, attributes
) VALUES (
    $1, $2, $3, $4, $5,
    $6::numeric, 'EA', $7::numeric, $8::numeric,
    COALESCE($9::jsonb, '{}'::jsonb)
);

-- name: InsertTender :exec
INSERT INTO t.transaction_tenders (
    tenant_id, transaction_id, tender_sequence, tender_type_id,
    amount, currency, processor_reference, attributes
) VALUES (
    $1, $2, $3, $4, $5::numeric, $6, $7,
    COALESCE($8::jsonb, '{}'::jsonb)
);

-- name: InsertDiscount :exec
INSERT INTO t.transaction_discounts (
    tenant_id, transaction_id, discount_sequence, scope,
    discount_type, amount, reason_code, attributes
) VALUES (
    $1, $2, $3, 'transaction', $4, $5::numeric, $6,
    COALESCE($7::jsonb, '{}'::jsonb)
);

-- name: GetTransactionByID :one
SELECT id, tenant_id, transaction_number, transaction_type,
       parent_transaction_id, location_id, pos_terminal_id,
       cashier_employee_id, customer_id, loyalty_membership_id,
       party_id, business_date::text, started_at, ended_at,
       status, ticket_number, item_count,
       subtotal::numeric, tax_total::numeric,
       discount_total::numeric, grand_total::numeric,
       currency, channel, is_training_mode, is_offline,
       void_reason, created_at, updated_at
  FROM t.transactions
 WHERE tenant_id = $1 AND id = $2;

-- name: GetTransactionByReceiptNumber :one
SELECT id, tenant_id, transaction_number, transaction_type,
       parent_transaction_id, location_id, pos_terminal_id,
       cashier_employee_id, customer_id, loyalty_membership_id,
       party_id, business_date::text, started_at, ended_at,
       status, ticket_number, item_count,
       subtotal::numeric, tax_total::numeric,
       discount_total::numeric, grand_total::numeric,
       currency, channel, is_training_mode, is_offline,
       void_reason, created_at, updated_at
  FROM t.transactions
 WHERE tenant_id = $1
   AND location_id = $2
   AND business_date = $3::date
   AND transaction_number = $4;

-- name: UpdateTransactionVoided :exec
UPDATE t.transactions
   SET status = 'voided', void_reason = $3, updated_at = now()
 WHERE tenant_id = $1 AND id = $2;

-- name: ListTransactions :many
SELECT id, tenant_id, transaction_number, transaction_type,
       parent_transaction_id, location_id, pos_terminal_id,
       cashier_employee_id, customer_id, loyalty_membership_id,
       party_id, business_date::text, started_at, ended_at,
       status, ticket_number, item_count,
       subtotal::numeric, tax_total::numeric,
       discount_total::numeric, grand_total::numeric,
       currency, channel, is_training_mode, is_offline,
       void_reason, created_at, updated_at
  FROM t.transactions
 WHERE tenant_id = $1
 ORDER BY started_at DESC
 LIMIT $2 OFFSET $3;

-- name: ListLineItemsByTransaction :many
SELECT id, transaction_id, line_number, item_id, description,
       quantity::numeric, unit_price::numeric,
       line_total::numeric, extended_tax::numeric, created_at
  FROM t.transaction_line_items
 WHERE transaction_id = $1
 ORDER BY line_number;

-- name: ListTendersByTransaction :many
SELECT id, transaction_id, tender_type_id,
       amount::numeric, currency, processor_reference, created_at
  FROM t.transaction_tenders
 WHERE transaction_id = $1
 ORDER BY tender_sequence;

-- name: ListDiscountsByTransaction :many
SELECT id, transaction_id, discount_type,
       amount::numeric, reason_code, created_at
  FROM t.transaction_discounts
 WHERE transaction_id = $1
 ORDER BY discount_sequence;
