//go:build integration

// Integration test for Sub 1 (Hash & Seal) — patent Application
// 63/991,596, Node 3. Wires the real stack: Postgres protocol.evidence
// + Valkey Streams + the worker. Run with:
//
//	GATEWAY_TEST_DATABASE_URL=postgres://growdirect:growdirect_dev@localhost:5432/canary_go_test?sslmode=disable \
//	GATEWAY_TEST_VALKEY_URL=redis://:valkey_dev@localhost:6379/2 \
//	go test -tags=integration -v ./internal/protocol/sub1/...
package sub1

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/protocol/publisher"
)

const (
	testSourceCode = "square"
)

func skipIfNoIntegration(t *testing.T) (dbURL, valkeyURL string) {
	t.Helper()
	dbURL = os.Getenv("GATEWAY_TEST_DATABASE_URL")
	valkeyURL = os.Getenv("GATEWAY_TEST_VALKEY_URL")
	if dbURL == "" || valkeyURL == "" {
		t.Skip("set GATEWAY_TEST_DATABASE_URL and GATEWAY_TEST_VALKEY_URL to run integration tests")
	}
	return
}

// seedMerchant inserts an org + merchant. Returns (merchantID, cleanup).
func seedMerchant(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (uuid.UUID, func()) {
	t.Helper()
	orgID := uuid.New()
	merchantID := uuid.New()
	sourceMerchantID := "test-source-" + merchantID.String()[:8]

	if _, err := pool.Exec(ctx,
		`INSERT INTO app.organizations (id, org_name) VALUES ($1, $2)`,
		orgID, "GRO-748 Sub1 Test Org"); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app.merchants (id, organization_id, source_merchant_id, merchant_name)
		 VALUES ($1, $2, $3, $4)`,
		merchantID, orgID, sourceMerchantID, "GRO-748 Sub1 Test Merchant"); err != nil {
		t.Fatalf("seed merchant: %v", err)
	}

	cleanup := func() {
		// protocol.evidence has triggers blocking DELETE — drop the
		// dependents first by truncating only test rows manually.
		// Trigger blocks DELETE, so we use a session_replication_role
		// flip to bypass triggers for cleanup. This stays inside the
		// test database; production roles cannot do this.
		_, _ = pool.Exec(ctx, `SET LOCAL session_replication_role = 'replica'`)
		_, _ = pool.Exec(ctx, `DELETE FROM protocol.evidence WHERE merchant_id = $1`, merchantID)
		_, _ = pool.Exec(ctx, `SET LOCAL session_replication_role = 'origin'`)
		_, _ = pool.Exec(ctx, `DELETE FROM app.merchants WHERE id = $1`, merchantID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.organizations WHERE id = $1`, orgID)
	}
	return merchantID, cleanup
}

func makeEvent(merchantID uuid.UUID, ingestedAt time.Time) publisher.Event {
	id := uuid.New()
	return publisher.Event{
		EventID:    id,
		EventHash:  fmt.Sprintf("eh-%s", id.String()),
		SourceCode: testSourceCode,
		MerchantID: merchantID,
		Timestamp:  ingestedAt,
		IngestedAt: ingestedAt,
		Payload:    json.RawMessage(`{"event":"order.created","amount":12345}`),
		Nonce:      uuid.NewString(),
	}
}

func TestIntegration_Sub1_EndToEnd_ChainContinuity(t *testing.T) {
	dbURL, valkeyURL := skipIfNoIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgx pool: %v", err)
	}
	defer pool.Close()

	opts, err := redis.ParseURL(valkeyURL)
	if err != nil {
		t.Fatalf("parse valkey: %v", err)
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()

	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("valkey ping: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("postgres ping: %v", err)
	}

	merchantID, cleanup := seedMerchant(t, ctx, pool)
	defer cleanup()

	// Isolated per-test stream + group so this test doesn't interfere
	// with other parallel runs.
	stream := "protocol:events:sub1-it"
	group := "sub1-it-" + uuid.NewString()[:8]
	_, _ = rdb.Del(ctx, stream).Result()

	logger := zap.NewNop()
	w := NewWorker(WorkerConfig{
		Pool:         pool,
		Redis:        rdb,
		Stream:       stream,
		Group:        group,
		Consumer:     "sub1-it-consumer",
		BlockTimeout: 200 * time.Millisecond,
		Logger:       logger,
	})
	if err := w.EnsureGroup(ctx); err != nil {
		t.Fatalf("ensure group: %v", err)
	}

	// Publish 3 events for the same merchant — direct to Streams,
	// using the same envelope shape the gateway uses.
	pub := publisher.NewValkey(rdb, stream)
	now := time.Now().UTC()
	events := []publisher.Event{
		makeEvent(merchantID, now),
		makeEvent(merchantID, now.Add(50*time.Millisecond)),
		makeEvent(merchantID, now.Add(100*time.Millisecond)),
	}
	for _, evt := range events {
		if err := pub.Publish(ctx, evt); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	// Drive the worker: process up to 3 batches.
	for i := 0; i < 3; i++ {
		if err := w.processBatch(ctx); err != nil {
			t.Fatalf("processBatch %d: %v", i, err)
		}
	}

	// Verify rows landed in protocol.evidence in correct order with
	// chained prev_hashes.
	rows, err := pool.Query(ctx, `
		SELECT event_id, event_hash, chain_hash,
		       COALESCE(prev_chain_hash, ''),
		       ingested_at
		FROM protocol.evidence
		WHERE merchant_id = $1
		ORDER BY ingested_at ASC
	`, merchantID)
	if err != nil {
		t.Fatalf("select evidence: %v", err)
	}
	defer rows.Close()

	type ev struct {
		eventID   uuid.UUID
		eventHash string
		chainHash string
		prevHash  string
		ingestAt  time.Time
	}
	var got []ev
	for rows.Next() {
		var e ev
		if err := rows.Scan(&e.eventID, &e.eventHash, &e.chainHash, &e.prevHash, &e.ingestAt); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, e)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 evidence rows, got %d", len(got))
	}
	if got[0].prevHash != "" {
		t.Errorf("first row prev should be empty, got %q", got[0].prevHash)
	}
	if got[1].prevHash != got[0].chainHash {
		t.Errorf("row2.prev=%s want=%s", got[1].prevHash, got[0].chainHash)
	}
	if got[2].prevHash != got[1].chainHash {
		t.Errorf("row3.prev=%s want=%s", got[2].prevHash, got[1].chainHash)
	}
	t.Logf("chain (3 events, same merchant):\n  1: %s\n  2: %s\n  3: %s",
		got[0].chainHash, got[1].chainHash, got[2].chainHash)

	// Cleanup the stream
	_, _ = rdb.Del(ctx, stream).Result()
}

func TestIntegration_Sub1_DuplicateEventHash_AcksAndSkips(t *testing.T) {
	dbURL, valkeyURL := skipIfNoIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, _ := pgxpool.New(ctx, dbURL)
	defer pool.Close()
	opts, _ := redis.ParseURL(valkeyURL)
	rdb := redis.NewClient(opts)
	defer rdb.Close()

	merchantID, cleanup := seedMerchant(t, ctx, pool)
	defer cleanup()

	stream := "protocol:events:sub1-dup-it"
	group := "sub1-dup-" + uuid.NewString()[:8]
	_, _ = rdb.Del(ctx, stream).Result()

	w := NewWorker(WorkerConfig{
		Pool:         pool,
		Redis:        rdb,
		Stream:       stream,
		Group:        group,
		Consumer:     "sub1-dup-consumer",
		BlockTimeout: 200 * time.Millisecond,
	})
	if err := w.EnsureGroup(ctx); err != nil {
		t.Fatalf("ensure group: %v", err)
	}

	pub := publisher.NewValkey(rdb, stream)
	evt := makeEvent(merchantID, time.Now().UTC())

	// Publish twice — same envelope, so same event_hash.
	if err := pub.Publish(ctx, evt); err != nil {
		t.Fatalf("publish 1: %v", err)
	}
	if err := pub.Publish(ctx, evt); err != nil {
		t.Fatalf("publish 2: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := w.processBatch(ctx); err != nil {
			t.Fatalf("batch %d: %v", i, err)
		}
	}

	// Exactly one row should exist.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM protocol.evidence WHERE merchant_id = $1`,
		merchantID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 evidence row after duplicate publish, got %d", count)
	}

	// Both stream entries should be ACKed (no pending).
	pending, err := rdb.XPending(ctx, stream, group).Result()
	if err != nil {
		t.Fatalf("xpending: %v", err)
	}
	if pending.Count != 0 {
		t.Errorf("expected 0 pending after duplicate handling, got %d", pending.Count)
	}

	_, _ = rdb.Del(ctx, stream).Result()
}

func TestIntegration_Sub1_TriggersBlockUpdateAndDelete(t *testing.T) {
	dbURL, _ := skipIfNoIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, _ := pgxpool.New(ctx, dbURL)
	defer pool.Close()

	merchantID, cleanup := seedMerchant(t, ctx, pool)
	defer cleanup()

	// Insert a row directly via WriteEvidence so we have something to
	// try to mutate.
	evt := makeEvent(merchantID, time.Now().UTC())
	if _, err := WriteEvidence(ctx, pool, evt); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}

	// UPDATE must be blocked.
	_, err := pool.Exec(ctx,
		`UPDATE protocol.evidence SET event_hash = 'tampered' WHERE event_id = $1`,
		evt.EventID)
	if err == nil {
		t.Fatal("UPDATE on protocol.evidence must be blocked by trigger, but succeeded")
	}
	t.Logf("UPDATE correctly blocked: %v", err)

	// DELETE must be blocked.
	_, err = pool.Exec(ctx,
		`DELETE FROM protocol.evidence WHERE event_id = $1`, evt.EventID)
	if err == nil {
		t.Fatal("DELETE on protocol.evidence must be blocked by trigger, but succeeded")
	}
	t.Logf("DELETE correctly blocked: %v", err)

	// TRUNCATE must be blocked too.
	_, err = pool.Exec(ctx, `TRUNCATE protocol.evidence`)
	if err == nil {
		t.Fatal("TRUNCATE must be blocked by trigger, but succeeded")
	}
	t.Logf("TRUNCATE correctly blocked: %v", err)
}
