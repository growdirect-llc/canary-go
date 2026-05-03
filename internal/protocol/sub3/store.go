package sub3

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EvidenceRow is a minimal projection of protocol.evidence used by
// the Sub 3 batch loop.
type EvidenceRow struct {
	EventHash string
	ChainHash string
}

// AnchorResult is the outcome of a successful WriteAnchor call.
type AnchorResult struct {
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

// AnchorStorer is the interface the Worker uses for DB operations.
// The real implementation is *Store; tests may inject a stub.
type AnchorStorer interface {
	WriteAnchor(ctx context.Context, inscriber Inscriber, batchSize, minBatch int) (*AnchorResult, error)
}

// Store wraps a pgxpool and provides the Sub 3 DB operations.
type Store struct {
	pool    *pgxpool.Pool
	network string
}

// NewStore constructs a Store. network is "signet" or "mainnet".
func NewStore(pool *pgxpool.Pool, network string) *Store {
	return &Store{pool: pool, network: network}
}

// lockAndFetchUnanchored returns up to batchSize evidence rows that have
// no entry in protocol.evidence_anchors. The query uses
// SELECT FOR UPDATE SKIP LOCKED so concurrent workers skip rows another
// worker has already locked.
//
// Must be called inside an open transaction — the lock is held for the
// lifetime of tx. The caller is responsible for committing or rolling back.
func lockAndFetchUnanchored(ctx context.Context, tx pgx.Tx, batchSize int) ([]EvidenceRow, error) {
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
	rows, err := tx.Query(ctx, q, batchSize)
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

// WriteAnchor drives the full fetch → inscribe → write cycle in two
// transactions to avoid holding a long-lived transaction across the
// external Inscribe HTTP call:
//
//   - Tx 1: lockAndFetchUnanchored → collect rows in memory → commit
//     (lock released, rows captured)
//   - External: Inscribe (network call, outside any transaction)
//   - Tx 2: INSERT into protocol.anchors + protocol.evidence_anchors → commit
//
// Returns (nil, nil) when the batch is below minBatch — no-op signal for
// the caller. Returns (*AnchorResult, nil) on success.
func (s *Store) WriteAnchor(
	ctx context.Context,
	inscriber Inscriber,
	batchSize, minBatch int,
) (*AnchorResult, error) {
	// ── Tx 1: fetch under lock, commit immediately ────────────────────────
	tx1, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("sub3: begin fetch tx: %w", err)
	}
	// Rollback is a no-op after Commit; always safe to defer.
	defer func() { _ = tx1.Rollback(ctx) }()

	rows, err := lockAndFetchUnanchored(ctx, tx1, batchSize)
	if err != nil {
		return nil, err
	}

	if len(rows) < minBatch {
		// Roll back — nothing to anchor yet. This is the clean no-op path.
		_ = tx1.Rollback(ctx)
		return nil, nil
	}

	// Commit Tx 1: rows are captured in memory; the FOR UPDATE lock is
	// released. Concurrent workers will skip these rows via evidence_anchors
	// once Tx 2 commits.
	if err := tx1.Commit(ctx); err != nil {
		return nil, fmt.Errorf("sub3: commit fetch tx: %w", err)
	}

	// ── Build Merkle tree ─────────────────────────────────────────────────
	leaves := make([]string, len(rows))
	for i, r := range rows {
		leaves[i] = r.ChainHash
	}
	merkleResult, err := BuildMerkleTree(leaves)
	if err != nil {
		return nil, err
	}

	// ── External: Inscribe (outside any transaction) ──────────────────────
	inscribeResult, err := inscriber.Inscribe(ctx, merkleResult.Root, s.network)
	if err != nil {
		// Record the failed attempt for the evidentiary audit trail.
		// evidence_anchors rows are NOT written — the events must remain
		// available for the next successful retry cycle.
		// WriteFailed is best-effort: if it fails the primary inscription
		// error is still returned to the caller. The next retry cycle will
		// produce another failed anchor record if needed.
		_ = s.WriteFailed(ctx, merkleResult.Root, len(rows))
		return nil, fmt.Errorf("sub3: inscribe: %w", err)
	}

	// ── Tx 2: write anchor + evidence_anchors ─────────────────────────────
	result, err := s.writeAnchorResults(ctx, rows, merkleResult, inscribeResult)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// writeAnchorResults opens a second transaction and writes the anchor record
// plus per-event proof rows. Idempotent via ON CONFLICT DO NOTHING on
// evidence_anchors.
func (s *Store) writeAnchorResults(
	ctx context.Context,
	rows []EvidenceRow,
	merkleResult MerkleResult,
	inscribeResult InscribeResult,
) (*AnchorResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("sub3: begin write tx: %w", err)
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
	// 0 is the sentinel for "not yet confirmed"; NULLIF maps it to NULL to match column semantics.
	var anchoredAt time.Time
	err = tx.QueryRow(ctx, insertAnchor,
		anchorID,
		merkleResult.Root,
		inscribeResult.InscriptionID,
		inscribeResult.TxID,
		inscribeResult.BlockHeight,
		s.network,
		len(rows),
		anchorStatus,
	).Scan(&anchoredAt)
	if err != nil {
		return nil, fmt.Errorf("sub3: insert anchor: %w", err)
	}

	const insertProof = `
		INSERT INTO protocol.evidence_anchors
			(event_hash, anchor_id, leaf_index, merkle_proof)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (event_hash, anchor_id) DO NOTHING
	`
	for i, row := range rows {
		proofJSON, err := json.Marshal(merkleResult.Proofs[i])
		if err != nil {
			return nil, fmt.Errorf("sub3: marshal proof[%d]: %w", i, err)
		}
		if _, err := tx.Exec(ctx, insertProof,
			row.EventHash,
			anchorID,
			i,
			proofJSON,
		); err != nil {
			return nil, fmt.Errorf("sub3: insert evidence_anchor[%d]: %w", i, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("sub3: commit write tx: %w", err)
	}

	return &AnchorResult{
		AnchorID:       anchorID,
		MerkleRoot:     merkleResult.Root,
		InscriptionID:  inscribeResult.InscriptionID,
		BtcTxID:        inscribeResult.TxID,
		BtcBlockHeight: inscribeResult.BlockHeight,
		Network:        s.network,
		EventCount:     len(rows),
		AnchorStatus:   anchorStatus,
		AnchoredAt:     anchoredAt,
	}, nil
}

// WriteFailed inserts a protocol.anchors row with anchor_status = 'failed'
// to record a failed inscription attempt for audit purposes. No
// evidence_anchors rows are inserted — those events remain available for
// the next successful retry cycle.
func (s *Store) WriteFailed(ctx context.Context, merkleRoot string, eventCount int) error {
	const q = `
		INSERT INTO protocol.anchors
			(anchor_id, merkle_root, inscription_id, btc_tx_id,
			 btc_block_height, network, event_count, anchor_status, anchored_at)
		VALUES (gen_random_uuid(), $1, NULL, NULL, NULL, $2, $3, 'failed', now())
	`
	_, err := s.pool.Exec(ctx, q, merkleRoot, s.network, eventCount)
	if err != nil {
		return fmt.Errorf("sub3: write failed anchor: %w", err)
	}
	return nil
}
