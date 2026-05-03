-- internal/db/sqlc/tsp.sql
--
-- TSP sequence-log queries. Table: protocol.tsp_sequence_log
-- (GRO-764 Phase A.2 + Wave C schema migration to canonical deploy/schema/).
--
-- The old queries (app.ingestion_log, sales.transactions) are archived
-- alongside the pre-Wave-A migrations in deploy/migrations/_archived/.

-- name: InsertTSPSequenceLog :one
INSERT INTO protocol.tsp_sequence_log (
    merchant_id, source_code, sequence_id, event_id,
    gap_detected, expected_prev_seq
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, merchant_id, source_code, sequence_id, event_id,
          received_at, gap_detected, expected_prev_seq;

-- name: GetTSPLastSequence :one
SELECT id, merchant_id, source_code, sequence_id, event_id,
       received_at, gap_detected, expected_prev_seq
  FROM protocol.tsp_sequence_log
 WHERE merchant_id = $1 AND source_code = $2
 ORDER BY received_at DESC
 LIMIT 1;

-- name: ListTSPGaps :many
SELECT id, merchant_id, source_code, sequence_id, event_id,
       received_at, gap_detected, expected_prev_seq
  FROM protocol.tsp_sequence_log
 WHERE merchant_id = $1 AND source_code = $2 AND gap_detected = true
 ORDER BY received_at DESC
 LIMIT $3;
