// Code generated from deploy/schema/11_protocol.sql for Loop 2.
// Wave 1 hand-written types — sqlc retrofit is Loop 3.
// Edit the SQL files in deploy/schema/, regenerate this file by hand.
package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// SourceSecret mirrors protocol.source_secrets.
type SourceSecret struct {
	ID                  uuid.UUID  `db:"id"`
	MerchantID          uuid.UUID  `db:"merchant_id"`
	SourceCode          string     `db:"source_code"`
	Secret              *string    `db:"secret"`
	SignatureAlgo       string     `db:"signature_algo"`
	Status              string     `db:"status"`
	ReplayWindowSeconds int32      `db:"replay_window_seconds"`
	CreatedAt           time.Time  `db:"created_at"`
	UpdatedAt           time.Time  `db:"updated_at"`
	RotatedAt           *time.Time `db:"rotated_at"`
	SecretSMRef         *string    `db:"secret_sm_ref"`
}

// Evidence mirrors protocol.evidence. Write-once L1 evidence chain.
type Evidence struct {
	EventID       uuid.UUID       `db:"event_id"`
	EventHash     string          `db:"event_hash"`
	ChainHash     string          `db:"chain_hash"`
	PrevChainHash *string         `db:"prev_chain_hash"`
	SourceCode    string          `db:"source_code"`
	MerchantID    uuid.UUID       `db:"merchant_id"`
	RawPayload    json.RawMessage `db:"raw_payload"`
	IngestedAt    time.Time       `db:"ingested_at"`
}
