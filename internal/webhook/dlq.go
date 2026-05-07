// internal/webhook/dlq.go
//
// Dead-letter queue persistence + replay.
//
// Rows in protocol.dlq are written when the gateway accepts a webhook
// (HMAC verified) but the downstream pipeline rejects the payload —
// Valkey publish failure, sub1 seal failure, sub2 parse failure, or
// any other recoverable error. Operators replay via the admin
// endpoint POST /v1/webhooks/replay/{id}; the cron-driven retry
// worker will pick rows up automatically once the
// next_retry_at watermark passes.
//
//

package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Backoff schedule (next_retry_at delta from now). After the final
// entry, status flips to 'abandoned' and the row needs operator
// intervention via the replay endpoint.
var defaultBackoff = []time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
}

// MaxAutoRetries is the count after which DLQ rows transition from
// 'pending' to 'abandoned'. Equal to len(defaultBackoff) — 4 retries.
const MaxAutoRetries = 4

// Errors. Handlers map ErrDLQNotFound to 404, ErrDLQTerminal to 409.
var (
	ErrDLQNotFound = errors.New("webhook: dlq row not found")
	ErrDLQTerminal = errors.New("webhook: dlq row in terminal status")
)

// DLQRow is the persisted shape of a dead-letter row.
type DLQRow struct {
	ID                uuid.UUID
	MerchantID        uuid.UUID
	SourceCode        string
	SourceEventID     *string
	EventID           *uuid.UUID
	Payload           json.RawMessage
	Headers           json.RawMessage
	FailureReason     string
	ErrorMessage      *string
	RetryCount        int
	NextRetryAt       *time.Time
	Status            string
	LastReplayAt      *time.Time
	LastReplayOutcome *string
	Attributes        json.RawMessage
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// DLQ is the pgxpool-backed accessor for protocol.dlq.
type DLQ struct {
	pool *pgxpool.Pool
}

// NewDLQ constructs a DLQ wrapping the given pool.
func NewDLQ(pool *pgxpool.Pool) *DLQ {
	return &DLQ{pool: pool}
}

// EnqueueParams captures the inputs to Enqueue. headers is marshaled
// as a JSON object; pass nil for no headers.
type EnqueueParams struct {
	MerchantID    uuid.UUID
	SourceCode    string
	SourceEventID *string
	EventID       *uuid.UUID
	Payload       json.RawMessage
	Headers       map[string]string
	FailureReason string
	ErrorMessage  string
}

// Enqueue inserts a new pending row into protocol.dlq. next_retry_at
// is set to now + first backoff entry.
func (q *DLQ) Enqueue(ctx context.Context, p EnqueueParams) (*DLQRow, error) {
	headersJSON, err := encodeHeaders(p.Headers)
	if err != nil {
		return nil, err
	}
	nextRetry := time.Now().Add(defaultBackoff[0])

	const insertQ = `
		INSERT INTO protocol.dlq
		    (merchant_id, source_code, source_event_id, event_id,
		     payload, headers, failure_reason, error_message,
		     retry_count, next_retry_at, status)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7, $8, 0, $9, 'pending')
		RETURNING id, merchant_id, source_code, source_event_id, event_id,
		          payload, headers, failure_reason, error_message,
		          retry_count, next_retry_at, status,
		          last_replay_at, last_replay_outcome, attributes,
		          created_at, updated_at`
	var em *string
	if p.ErrorMessage != "" {
		s := p.ErrorMessage
		em = &s
	}
	row := q.pool.QueryRow(ctx, insertQ,
		p.MerchantID, p.SourceCode, p.SourceEventID, p.EventID,
		p.Payload, headersJSON, p.FailureReason, em, nextRetry,
	)
	return scanDLQ(row)
}

// Get returns the row by id. ErrDLQNotFound if absent.
func (q *DLQ) Get(ctx context.Context, id uuid.UUID) (*DLQRow, error) {
	const sql = `
		SELECT id, merchant_id, source_code, source_event_id, event_id,
		       payload, headers, failure_reason, error_message,
		       retry_count, next_retry_at, status,
		       last_replay_at, last_replay_outcome, attributes,
		       created_at, updated_at
		  FROM protocol.dlq
		 WHERE id = $1`
	row := q.pool.QueryRow(ctx, sql, id)
	out, err := scanDLQ(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDLQNotFound
		}
		return nil, err
	}
	return out, nil
}

// ListFilters captures the filter shape for List.
type ListFilters struct {
	MerchantID *uuid.UUID
	SourceCode string
	Status string // pending | replayed | abandoned; empty = any
	Limit int // default 50, max 200
	Offset     int
}

// List returns DLQ rows matching the filters, ordered by created_at
// DESC (most recent first).
func (q *DLQ) List(ctx context.Context, f ListFilters) ([]DLQRow, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	args := []any{}
	where := "WHERE 1=1"
	if f.MerchantID != nil {
		args = append(args, *f.MerchantID)
		where += fmt.Sprintf(" AND merchant_id = $%d", len(args))
	}
	if f.SourceCode != "" {
		args = append(args, f.SourceCode)
		where += fmt.Sprintf(" AND source_code = $%d", len(args))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		where += fmt.Sprintf(" AND status = $%d", len(args))
	}
	args = append(args, f.Limit, f.Offset)
	sql := fmt.Sprintf(`
		SELECT id, merchant_id, source_code, source_event_id, event_id,
		       payload, headers, failure_reason, error_message,
		       retry_count, next_retry_at, status,
		       last_replay_at, last_replay_outcome, attributes,
		       created_at, updated_at
		  FROM protocol.dlq
		  %s
		 ORDER BY created_at DESC
		 LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args))

	rows, err := q.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("webhook: list dlq: %w", err)
	}
	defer rows.Close()
	out := make([]DLQRow, 0, f.Limit)
	for rows.Next() {
		r, err := scanDLQ(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// MarkReplayed records a successful replay. status → 'replayed';
// next_retry_at cleared. last_replay_outcome = 'success'.
func (q *DLQ) MarkReplayed(ctx context.Context, id uuid.UUID) error {
	const sql = `
		UPDATE protocol.dlq
		   SET status = 'replayed',
		       next_retry_at = NULL,
		       last_replay_at = now(),
		       last_replay_outcome = 'success',
		       updated_at = now()
		 WHERE id = $1`
	tag, err := q.pool.Exec(ctx, sql, id)
	if err != nil {
		return fmt.Errorf("webhook: dlq mark replayed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDLQNotFound
	}
	return nil
}

// MarkRetryFailed records a failed replay attempt. retry_count is
// incremented; next_retry_at is bumped to the next backoff entry.
// When retry_count exceeds MaxAutoRetries, status flips to
// 'abandoned' and next_retry_at is cleared.
func (q *DLQ) MarkRetryFailed(ctx context.Context, id uuid.UUID, errorMessage string) (*DLQRow, error) {
	tx, err := q.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("webhook: dlq retry failed begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cur, err := q.txGet(ctx, tx, id)
	if err != nil {
		return nil, err
	}

	cur.RetryCount++
	em := errorMessage
	cur.ErrorMessage = &em
	now := time.Now()
	cur.LastReplayAt = &now
	failOutcome := "failure"
	cur.LastReplayOutcome = &failOutcome

	if cur.RetryCount >= MaxAutoRetries {
		cur.Status = "abandoned"
		cur.NextRetryAt = nil
	} else {
		next := time.Now().Add(defaultBackoff[cur.RetryCount])
		cur.NextRetryAt = &next
	}

	const sql = `
		UPDATE protocol.dlq
		   SET retry_count = $2,
		       next_retry_at = $3,
		       status = $4,
		       error_message = $5,
		       last_replay_at = $6,
		       last_replay_outcome = $7,
		       updated_at = now()
		 WHERE id = $1
		RETURNING id, merchant_id, source_code, source_event_id, event_id,
		          payload, headers, failure_reason, error_message,
		          retry_count, next_retry_at, status,
		          last_replay_at, last_replay_outcome, attributes,
		          created_at, updated_at`
	row := tx.QueryRow(ctx, sql,
		id, cur.RetryCount, cur.NextRetryAt, cur.Status,
		cur.ErrorMessage, cur.LastReplayAt, cur.LastReplayOutcome,
	)
	out, err := scanDLQ(row)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("webhook: dlq commit: %w", err)
	}
	return out, nil
}

// txGet fetches a row inside an open transaction with FOR UPDATE.
func (q *DLQ) txGet(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*DLQRow, error) {
	const sql = `
		SELECT id, merchant_id, source_code, source_event_id, event_id,
		       payload, headers, failure_reason, error_message,
		       retry_count, next_retry_at, status,
		       last_replay_at, last_replay_outcome, attributes,
		       created_at, updated_at
		  FROM protocol.dlq
		 WHERE id = $1
		   FOR UPDATE`
	row := tx.QueryRow(ctx, sql, id)
	out, err := scanDLQ(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDLQNotFound
		}
		return nil, err
	}
	if out.Status != "pending" {
		return nil, fmt.Errorf("%w: %s", ErrDLQTerminal, out.Status)
	}
	return out, nil
}

// scannable is the small subset of pgx.Row / pgx.Rows that scanDLQ
// needs. Allows sharing scan logic across QueryRow + Query iterations.
type scannable interface {
	Scan(...any) error
}

func scanDLQ(r scannable) (*DLQRow, error) {
	var d DLQRow
	if err := r.Scan(
		&d.ID, &d.MerchantID, &d.SourceCode, &d.SourceEventID, &d.EventID,
		&d.Payload, &d.Headers, &d.FailureReason, &d.ErrorMessage,
		&d.RetryCount, &d.NextRetryAt, &d.Status,
		&d.LastReplayAt, &d.LastReplayOutcome, &d.Attributes,
		&d.CreatedAt, &d.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &d, nil
}

func encodeHeaders(h map[string]string) (json.RawMessage, error) {
	if h == nil {
		return json.RawMessage("{}"), nil
	}
	b, err := json.Marshal(h)
	if err != nil {
		return nil, fmt.Errorf("webhook: encode headers: %w", err)
	}
	return b, nil
}
