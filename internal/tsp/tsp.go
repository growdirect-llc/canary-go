// Package tsp provides Telemetry Sequence Protocol primitives for
// the Canary Go ingest pipeline. Sources that supply a sequence id
// per event get gap detection + replay-by-sequence; sources that
// don't supply one fall back to canonical event_hash dedup at sub1.
//
// Spec: GRO-764 Phase A.2 (folds part of GRO-642 epic). Companion
// SDDs: docs/sdds/go-handoff/tsp.md, tsp-seal.md, tsp-parse.md,
// feed-tier-contract.md.
//
// This package owns the protocol.tsp_sequence_log persistence —
// recording the sequence number, detecting gaps against the prior
// row, and exposing query helpers for the replay path.
package tsp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sentinel errors for the gap-detection + replay paths.
var (
	ErrSequenceNotFound = errors.New("tsp: sequence not found")
	ErrDuplicateSequence = errors.New("tsp: sequence already recorded")
)

// SequenceEntry is the persisted shape of a row in
// protocol.tsp_sequence_log.
type SequenceEntry struct {
	ID              uuid.UUID
	MerchantID      uuid.UUID
	SourceCode      string
	SequenceID      string
	EventID         uuid.UUID
	ReceivedAt      time.Time
	GapDetected     bool
	ExpectedPrevSeq *string
}

// SequenceLog is the pgxpool-backed accessor for protocol.tsp_sequence_log.
type SequenceLog struct {
	pool *pgxpool.Pool
	// expectedPrevFn computes what the previous sequence should be
	// given the current one. Default: lexicographic-1 fallback when
	// the sequence id parses as an integer; otherwise nil.
	expectedPrevFn func(current string) *string
}

// NewSequenceLog constructs a SequenceLog with sensible defaults.
func NewSequenceLog(pool *pgxpool.Pool, opts ...Option) *SequenceLog {
	s := &SequenceLog{
		pool:           pool,
		expectedPrevFn: defaultExpectedPrev,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Option configures a SequenceLog at construction.
type Option func(*SequenceLog)

// WithExpectedPrevFn supplies a custom function to compute the
// expected previous sequence id given the current one. The default
// handles integer sequences (subtract 1); sources with non-integer
// monotonic ids should provide their own.
func WithExpectedPrevFn(f func(current string) *string) Option {
	return func(s *SequenceLog) { s.expectedPrevFn = f }
}

// Record persists a sequence entry. Returns ErrDuplicateSequence
// when the (merchant_id, source_code, sequence_id) tuple already
// exists. Gap detection runs as a side check against the most-recent
// prior entry for the same (merchant_id, source_code) pair.
func (s *SequenceLog) Record(
	ctx context.Context,
	merchantID uuid.UUID,
	sourceCode, sequenceID string,
	eventID uuid.UUID,
) (*SequenceEntry, error) {
	if sequenceID == "" {
		return nil, errors.New("tsp: sequence_id required")
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("tsp: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Look up the most-recent prior entry for this (merchant, source) pair.
	var lastSeq string
	err = tx.QueryRow(ctx, `
		SELECT sequence_id
		  FROM protocol.tsp_sequence_log
		 WHERE merchant_id = $1
		   AND source_code = $2
		 ORDER BY received_at DESC
		 LIMIT 1`, merchantID, sourceCode).Scan(&lastSeq)
	hasLast := true
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			hasLast = false
		} else {
			return nil, fmt.Errorf("tsp: prev seq lookup: %w", err)
		}
	}

	gap := false
	var expectedPrev *string
	if hasLast {
		expectedPrev = s.expectedPrevFn(sequenceID)
		if expectedPrev != nil && *expectedPrev != lastSeq {
			gap = true
		}
	}

	const insertQ = `
		INSERT INTO protocol.tsp_sequence_log
		    (merchant_id, source_code, sequence_id, event_id,
		     gap_detected, expected_prev_seq)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, merchant_id, source_code, sequence_id, event_id,
		          received_at, gap_detected, expected_prev_seq`
	row := tx.QueryRow(ctx, insertQ, merchantID, sourceCode, sequenceID, eventID, gap, expectedPrev)
	var e SequenceEntry
	if err := row.Scan(&e.ID, &e.MerchantID, &e.SourceCode, &e.SequenceID,
		&e.EventID, &e.ReceivedAt, &e.GapDetected, &e.ExpectedPrevSeq); err != nil {
		// Duplicate (merchant, source, sequence) tuple — UNIQUE
		// constraint violation surfaces as 23505. We don't need to
		// inspect the SQLSTATE specifically; the wrapped error tells
		// the caller to treat it as duplicate.
		return nil, fmt.Errorf("%w: %v", ErrDuplicateSequence, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("tsp: commit: %w", err)
	}
	return &e, nil
}

// GetGaps returns sequence entries where gap_detected = TRUE for the
// given merchant + source, ordered by received_at ASC.
func (s *SequenceLog) GetGaps(ctx context.Context, merchantID uuid.UUID, sourceCode string, limit int) ([]SequenceEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `
		SELECT id, merchant_id, source_code, sequence_id, event_id,
		       received_at, gap_detected, expected_prev_seq
		  FROM protocol.tsp_sequence_log
		 WHERE merchant_id = $1
		   AND source_code = $2
		   AND gap_detected = TRUE
		 ORDER BY received_at ASC
		 LIMIT $3`
	rows, err := s.pool.Query(ctx, q, merchantID, sourceCode, limit)
	if err != nil {
		return nil, fmt.Errorf("tsp: gaps query: %w", err)
	}
	defer rows.Close()
	out := make([]SequenceEntry, 0, limit)
	for rows.Next() {
		var e SequenceEntry
		if err := rows.Scan(&e.ID, &e.MerchantID, &e.SourceCode, &e.SequenceID,
			&e.EventID, &e.ReceivedAt, &e.GapDetected, &e.ExpectedPrevSeq); err != nil {
			return nil, fmt.Errorf("tsp: gaps scan: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LookupBySequence returns the entry for a (merchant, source,
// sequence) tuple. Used by the replay-by-sequence path: ops requests
// "replay event whose sequence was X" and we need the canonical
// event_id to feed back into the pipeline.
func (s *SequenceLog) LookupBySequence(ctx context.Context, merchantID uuid.UUID, sourceCode, sequenceID string) (*SequenceEntry, error) {
	const q = `
		SELECT id, merchant_id, source_code, sequence_id, event_id,
		       received_at, gap_detected, expected_prev_seq
		  FROM protocol.tsp_sequence_log
		 WHERE merchant_id = $1
		   AND source_code = $2
		   AND sequence_id = $3`
	row := s.pool.QueryRow(ctx, q, merchantID, sourceCode, sequenceID)
	var e SequenceEntry
	if err := row.Scan(&e.ID, &e.MerchantID, &e.SourceCode, &e.SequenceID,
		&e.EventID, &e.ReceivedAt, &e.GapDetected, &e.ExpectedPrevSeq); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSequenceNotFound
		}
		return nil, fmt.Errorf("tsp: lookup: %w", err)
	}
	return &e, nil
}

// defaultExpectedPrev computes the expected previous sequence for an
// integer-monotonic sequence id (the most common case across POS
// vendors). Returns nil for non-integer sequences — caller should
// supply WithExpectedPrevFn for those.
func defaultExpectedPrev(current string) *string {
	var n int64
	if _, err := fmt.Sscanf(current, "%d", &n); err != nil {
		return nil
	}
	if n <= 0 {
		return nil
	}
	prev := fmt.Sprintf("%d", n-1)
	return &prev
}
