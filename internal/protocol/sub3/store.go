package sub3

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EvidenceRow is a minimal projection of protocol.evidence used by
// the Sub 3 batch loop.
type EvidenceRow struct {
	EventHash string
	ChainHash string
}

// AnchorRecord is the full record written to protocol.anchors.
type AnchorRecord struct {
	AnchorID      uuid.UUID
	MerkleRoot    string
	InscriptionID string
	BtcTxID       string
	BtcBlockHeight int64
	Network       string
	EventCount    int
	AnchorStatus  string
	AnchoredAt    time.Time
}

// LockAndFetchUnanchored returns up to batchSize evidence rows that
// have no entry in protocol.evidence_anchors, using
// SELECT FOR UPDATE SKIP LOCKED to prevent concurrent workers from
// processing the same batch. Returns an empty slice if there is nothing
// to anchor.
//
// Note: SKIP LOCKED on protocol.evidence requires a transaction that
// the caller holds open. We keep this simple for Sub 3 v1: fetch inside
// the caller's transaction context. The worker passes a tx-capable pool.
func LockAndFetchUnanchored(ctx context.Context, pool *pgxpool.Pool, batchSize int) ([]EvidenceRow, error) {
	const q = `
		SELECT e.event_hash, e.chain_hash
		FROM protocol.evidence e
		WHERE NOT EXISTS (
			SELECT 1
			FROM protocol.evidence_anchors ea
			WHERE ea.event_hash = e.event_hash
		)
		ORDER BY e.ingested_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`
	rows, err := pool.Query(ctx, q, batchSize)
	if err != nil {
		return nil, fmt.Errorf("sub3: fetch unanchored: %w", err)
	}
	defer rows.Close()

	var result []EvidenceRow
	for rows.Next() {
		var r EvidenceRow
		if err := rows.Scan(&r.EventHash, &r.ChainHash); err != nil {
			return nil, fmt.Errorf("sub3: scan evidence row: %w", err)
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sub3: rows error: %w", err)
	}
	return result, nil
}

// WriteAnchor inserts one protocol.anchors row and the corresponding
// protocol.evidence_anchors rows in a single transaction.
//
// Idempotent via ON CONFLICT DO NOTHING on evidence_anchors: if the
// worker crashes after a partial write and retries, the already-written
// proof rows are silently skipped.
func WriteAnchor(
	ctx context.Context,
	pool *pgxpool.Pool,
	result MerkleResult,
	rows []EvidenceRow,
	inscribeResult InscribeResult,
	network string,
) (AnchorRecord, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return AnchorRecord{}, fmt.Errorf("sub3: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	anchorID := uuid.New()
	anchorStatus := "pending"
	if inscribeResult.InscriptionID != "" {
		anchorStatus = "inscribed"
	}

	const insertAnchor = `
		INSERT INTO protocol.anchors
			(anchor_id, merkle_root, inscription_id, btc_tx_id,
			 btc_block_height, network, event_count, anchor_status, anchored_at)
		VALUES ($1, $2, NULLIF($3,''), NULLIF($4,''),
			NULLIF($5::bigint, 0), $6, $7, $8, now())
		RETURNING anchored_at
	`
	var anchoredAt time.Time
	err = tx.QueryRow(ctx, insertAnchor,
		anchorID,
		result.Root,
		inscribeResult.InscriptionID,
		inscribeResult.TxID,
		inscribeResult.BlockHeight,
		network,
		len(rows),
		anchorStatus,
	).Scan(&anchoredAt)
	if err != nil {
		return AnchorRecord{}, fmt.Errorf("sub3: insert anchor: %w", err)
	}

	const insertProof = `
		INSERT INTO protocol.evidence_anchors
			(event_hash, anchor_id, leaf_index, merkle_proof)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (event_hash, anchor_id) DO NOTHING
	`
	for i, row := range rows {
		proofJSON, err := json.Marshal(result.Proofs[i])
		if err != nil {
			return AnchorRecord{}, fmt.Errorf("sub3: marshal proof[%d]: %w", i, err)
		}
		if _, err := tx.Exec(ctx, insertProof,
			row.EventHash,
			anchorID,
			i,
			proofJSON,
		); err != nil {
			return AnchorRecord{}, fmt.Errorf("sub3: insert evidence_anchor[%d]: %w", i, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return AnchorRecord{}, fmt.Errorf("sub3: commit: %w", err)
	}

	return AnchorRecord{
		AnchorID:       anchorID,
		MerkleRoot:     result.Root,
		InscriptionID:  inscribeResult.InscriptionID,
		BtcTxID:        inscribeResult.TxID,
		BtcBlockHeight: inscribeResult.BlockHeight,
		Network:        network,
		EventCount:     len(rows),
		AnchorStatus:   anchorStatus,
		AnchoredAt:     anchoredAt,
	}, nil
}
